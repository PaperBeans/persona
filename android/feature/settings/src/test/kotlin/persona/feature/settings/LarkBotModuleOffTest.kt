package persona.feature.settings

import android.content.Context
import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import androidx.compose.ui.unit.dp
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.JsonObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotSame
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.LarkFeatures
import persona.core.contracts.Machine
import persona.core.contracts.ServerEvent
import persona.core.contracts.Session
import persona.core.contracts.api.*
import persona.core.designsystem.AppStyle
import persona.core.designsystem.LocalHubFeatures
import persona.core.designsystem.LocalLarkFeatures
import persona.core.designsystem.PersonaTheme
import persona.core.moduleapi.HubModule
import java.io.File

/** Hello features of a hub that publishes module flags, with the Lark extension installed: Lark module on, and off. */
private val LarkOn = setOf("modules", "module.lark", "lark", "channels")
private val LarkOff = setOf("modules", "module.recording", "lark", "channels")

private fun channel(id: String, name: String, kind: String = "im", readOnly: Boolean = false, state: String = "connected") =
    HubChannel(id, name, kind, "builtin", installed = true, enabled = true, state = state, bind = if (readOnly) "config" else "fields",
        readOnly = readOnly, description = "$name description", target = ChannelTarget.Main,
        features = listOf(ChannelFeature("main", "Feature of $name", "", true)))

/** The hub's list: Persona's apps, the Lark bot (broken, as it reads on a hub whose module is off), /agent in Lark, a chat bot. */
private val HubChannels = listOf(
    channel("app", "Persona", kind = "app", readOnly = true),
    channel("feishu", "Lark bot", state = "error"),
    channel("lark_agent", "Lark /agent", kind = "mention", readOnly = true, state = "disabled"),
    channel("chat", "Example Chat"),
)

/** Counts what is asked of the hub, by channel id: a page that must not ask shows as zero. */
private class RecordingChannels : ChannelManagement {
    override val available = MutableStateFlow(true)
    val reads = mutableListOf<String>()
    val writes = mutableListOf<String>()
    override suspend fun channels() = HubChannels
    override suspend fun channel(id: String): ChannelDetail {
        reads += id
        return ChannelDetail(HubChannels.first { it.id == id }, ChannelWaiting(),
            settingsUrl = "settings/connectors/feishu-bot".takeIf { id == "feishu" })
    }
    override suspend fun update(id: String, change: ChannelChange) = channel(id).also { writes += id }
    override suspend fun bind(id: String, step: String?, values: Map<String, String>?): BindPrompt { writes += id; return BindPrompt("done", done = true) }
    override suspend fun unbind(id: String) = channel(id).also { writes += id }
    override suspend fun test(id: String) { writes += id }
}

private class RecordingBot : FeishuBotSettings {
    var reads = 0
    val updates = mutableListOf<FeishuBotUpdate>()
    private val status = FeishuBotStatus(enabled = true, appId = "cli_a1b2c3", hasSecret = true, connected = false, botName = "Example bot")
    override suspend fun status(): FeishuBotStatus { reads++; return status }
    override suspend fun update(update: FeishuBotUpdate): FeishuBotStatus { updates += update; return status }
}

private fun services(channels: ChannelManagement, bot: FeishuBotSettings? = null) = SettingsServices(
    pairing = object : PairingService {
        override val isPaired = MutableStateFlow(true)
        override suspend fun pair(request: PairingRequest) = error("not used")
        override suspend fun unpair() = error("not used")
    },
    connection = object : HubConnection {
        override val state = MutableStateFlow(ConnectionState.Online)
        override val events = MutableSharedFlow<ServerEvent>()
        override fun reconnect() = error("not used")
        override fun send(message: JsonObject) = false
    },
    sessions = object : SessionsRepository {
        override val machines = MutableStateFlow(emptyList<Machine>())
        override val sessions = MutableStateFlow(emptyList<Session>())
        override suspend fun refresh() {}
    },
    updates = object : AppUpdates { override suspend fun check(): AppUpdate? = null },
    channels = channels, feishuBot = bot,
)

/**
 * Settings follows the hub's Lark module (#15995, the rule of #13430): with `module.lark` off the Lark bot's entry points
 * are gone (its channel in Settings › Channels, that channel's page, the bot's own page) and a page reached anyway says
 * so without asking the hub anything; what the Lark extension shows stays; with the module on, or a hub that says
 * nothing about modules, everything is as it was.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "zh-w400dp-h1500dp-xhdpi")
class LarkBotModuleOffTest {
    @get:Rule val rule = createComposeRule()

    private fun text(id: Int): String = ApplicationProvider.getApplicationContext<Context>().getString(id)
    private val offLine get() = text(R.string.settings_lark_bot_module_off)

    private fun show(features: Set<String>, content: @androidx.compose.runtime.Composable () -> Unit) = rule.setContent {
        PersonaTheme(reduceMotion = true) {
            CompositionLocalProvider(LocalHubFeatures provides features, LocalLarkFeatures provides LarkFeatures.of(features)) {
                Box(Modifier.width(400.dp).height(1500.dp)) { content() }
            }
        }
    }

    private fun shown(text: String) = rule.onAllNodesWithText(text).fetchSemanticsNodes().isNotEmpty()
    private fun waitFor(text: String) = rule.waitUntil(5_000) { shown(text) }

    @Test fun theEntryFollowsTheModuleFlagAndNothingElse() {
        // No hello yet, the demo, an older hub without the marker: offered.
        assertEquals(LarkBotEntry.Offered, LarkBotEntry.of(emptySet()))
        assertEquals(LarkBotEntry.Offered, LarkBotEntry.of(setOf("channels", "lark", "lark_attachments")))
        assertEquals(LarkBotEntry.Offered, LarkBotEntry.of(LarkOn))
        assertEquals(LarkBotEntry.ModuleOff, LarkBotEntry.of(LarkOff))
        // The Lark extension's feature is not the module's: it neither offers the bot nor takes it away.
        assertEquals(LarkBotEntry.ModuleOff, LarkBotEntry.of(setOf("modules", "lark", "lark_moments", "lark_doc_comments")))
        assertEquals(LarkBotEntry.Offered, LarkBotEntry.of(setOf("modules", "module.lark")))
        // And the module's flag does not change what the extension's surfaces can do.
        assertEquals(LarkFeatures.of(LarkOn - "module.lark"), LarkFeatures.of(LarkOn))
        assertTrue(LarkFeatures.of(LarkOff).chats)
        assertEquals("module.lark", HubModule.Lark.feature)
    }

    @Test fun offTakesOnlyTheBotsChannelOutOfTheList() {
        assertEquals(HubChannels, LarkBotEntry.Offered.listed(HubChannels))
        assertEquals(listOf("app", "lark_agent", "chat"), LarkBotEntry.ModuleOff.listed(HubChannels).map { it.id })
        assertTrue(LarkBotEntry.ModuleOff.hides("feishu"))
        assertFalse(LarkBotEntry.ModuleOff.hides("lark_agent"))
        assertFalse(LarkBotEntry.ModuleOff.hides("chat"))
        assertFalse(LarkBotEntry.Offered.hides("feishu"))
        val hub = RecordingChannels()
        // On: the same object, so nothing about the page's models or their keys changes.
        assertSame(hub, LarkBotEntry.Offered.listing(hub))
        val off = LarkBotEntry.ModuleOff.listing(hub)
        assertNotSame(hub, off)
        assertEquals(listOf("app", "lark_agent", "chat"), runBlocking { off.channels() }.map { it.id })
        // Everything else is the hub's own answer.
        assertSame(hub.available, off.available)
        assertEquals("chat", runBlocking { off.channel("chat") }.channel.id)
        // The home row's count no longer carries a bot that cannot be fixed from here.
        val res = ApplicationProvider.getApplicationContext<Context>().resources
        assertTrue(ChannelText.hasProblem(HubChannels))
        assertFalse(ChannelText.hasProblem(LarkBotEntry.ModuleOff.listed(HubChannels)))
        assertEquals(ChannelText.listSummary(res, HubChannels - HubChannels[1]), ChannelText.listSummary(res, LarkBotEntry.ModuleOff.listed(HubChannels)))
    }

    @Test fun withTheModuleOnTheChannelsListIsTheHubs() {
        var features by mutableStateOf(LarkOn)
        show(emptySet()) { CompositionLocalProvider(LocalHubFeatures provides features) { key(features) { SettingsScreen(services(RecordingChannels()), initialPage = "channels") } } }
        for (next in listOf(LarkOn, emptySet(), setOf("channels", "lark"))) {
            features = next
            waitFor("Example Chat")
            rule.onAllNodes(hasText("Lark bot") and hasClickAction()).assertCountEquals(1)
            rule.onAllNodes(hasText("Lark /agent") and hasClickAction()).assertCountEquals(1)
            rule.onAllNodesWithText(offLine).assertCountEquals(0)
        }
    }

    @Test fun withTheModuleOffTheBotsChannelIsNotListedAndTheOthersAre() {
        show(LarkOff) { SettingsScreen(services(RecordingChannels()), initialPage = "channels") }
        waitFor("Example Chat")
        rule.onAllNodesWithText("Lark bot").assertCountEquals(0)
        // /agent in Lark is the Lark extension's and Persona's apps are Persona's: both stay.
        rule.onAllNodes(hasText("Lark /agent") and hasClickAction()).assertCountEquals(1)
        rule.onAllNodes(hasText("Persona") and hasClickAction()).assertCountEquals(1)
        // The list itself needs no explanation: the row is simply not there.
        rule.onAllNodesWithText(offLine).assertCountEquals(0)
    }

    @Test fun theListFollowsAHelloThatChanges() {
        var features by mutableStateOf(LarkOn)
        val hub = RecordingChannels()
        rule.setContent {
            PersonaTheme(reduceMotion = true) {
                CompositionLocalProvider(LocalHubFeatures provides features) {
                    Box(Modifier.width(400.dp).height(1500.dp)) { SettingsScreen(services(hub), initialPage = "channels") }
                }
            }
        }
        waitFor("Lark bot")
        features = LarkOff
        rule.waitUntil(5_000) { !shown("Lark bot") }
        assertTrue(shown("Example Chat"))
        features = LarkOn
        waitFor("Lark bot")
    }

    @Test fun aLinkToTheBotsChannelSaysItIsOffAndAsksTheHubNothing() {
        val hub = RecordingChannels()
        show(LarkOff) { ChannelPage(hub, "feishu", onBack = {}, onOpen = {}, larkBot = rememberLarkBotEntry()) }
        waitFor(offLine)
        rule.onAllNodesWithText(text(R.string.settings_feishu_bot_title)).assertCountEquals(1)
        for (gone in listOf(R.string.settings_src_enable, R.string.settings_src_channel_test, R.string.settings_src_channel_more_settings))
            rule.onAllNodesWithText(text(gone)).assertCountEquals(0)
        rule.onAllNodesWithText(text(R.string.settings_src_retry)).assertCountEquals(0)
        rule.waitForIdle()
        assertEquals(emptyList<String>(), hub.reads)
        assertEquals(emptyList<String>(), hub.writes)
    }

    @Test fun anotherChannelsPageIsUntouchedWithTheModuleOff() {
        val hub = RecordingChannels()
        show(LarkOff) { ChannelPage(hub, "chat", onBack = {}, onOpen = {}, larkBot = rememberLarkBotEntry()) }
        waitFor(text(R.string.settings_src_enable))
        rule.onAllNodesWithText(offLine).assertCountEquals(0)
        assertEquals(listOf("chat"), hub.reads.distinct())
    }

    @Test fun withTheModuleOnTheBotsChannelPageIsAsItWas() {
        val hub = RecordingChannels()
        var opened: androidx.navigation3.runtime.NavKey? = null
        show(LarkOn) { ChannelPage(hub, "feishu", onBack = {}, onOpen = { opened = it }, larkBot = rememberLarkBotEntry()) }
        waitFor(text(R.string.settings_src_enable))
        rule.onAllNodesWithText(offLine).assertCountEquals(0)
        rule.onNode(hasText(text(R.string.settings_src_channel_more_settings)) and hasClickAction()).performScrollTo().performClick()
        assertEquals(persona.core.navigation.SettingsPageRoute(persona.core.navigation.SettingsPage.FeishuBot), opened)
        assertEquals(listOf("feishu"), hub.reads.distinct())
    }

    @Test fun aLinkToTheBotsPageSaysItIsOffAndAsksTheHubNothing() {
        val bot = RecordingBot()
        // The page as a deep link, a saved page or the debug launch opens it: Settings started on "feishu-bot".
        show(LarkOff) { SettingsScreen(services(RecordingChannels(), bot), initialPage = "feishu-bot", onBack = {}) }
        waitFor(offLine)
        rule.onAllNodesWithText(text(R.string.settings_feishu_bot_title)).assertCountEquals(1)
        for (gone in listOf(R.string.settings_feishu_bot_enable, R.string.settings_feishu_bot_credentials))
            rule.onAllNodesWithText(text(gone)).assertCountEquals(0)
        rule.waitForIdle()
        assertEquals(0, bot.reads)
        assertEquals(emptyList<FeishuBotUpdate>(), bot.updates)
    }

    private fun theBotsPageIsAsItWas(features: Set<String>) {
        val bot = RecordingBot()
        show(features) { SettingsScreen(services(RecordingChannels(), bot), initialPage = "feishu-bot", onBack = {}) }
        waitFor(text(R.string.settings_feishu_bot_enable))
        waitFor("Example bot")
        rule.onAllNodesWithText(offLine).assertCountEquals(0)
        assertTrue(bot.reads > 0)
    }

    @Test fun withTheModuleOnTheBotsPageIsAsItWas() = theBotsPageIsAsItWas(LarkOn)

    @Test fun onAHubThatSaysNothingAboutModulesTheBotsPageIsAsItWas() = theBotsPageIsAsItWas(setOf("channels", "lark"))

    @Test fun beforeAHelloTheBotsPageIsAsItWas() = theBotsPageIsAsItWas(emptySet())

    /** What the user already has: the Lark extension's row in Settings › Connectors does not read the module's flag. */
    @Test fun theLarkConnectorRowStaysWithTheModuleOff() {
        show(LarkOff) { SettingsScreen(services(RecordingChannels()), initialPage = "connectors", onBack = {}) }
        waitFor(text(R.string.settings_row_lark_subtitle))
        rule.onAllNodes(hasText(text(R.string.settings_row_lark)) and hasClickAction()).assertCountEquals(1)
        rule.onAllNodesWithText(offLine).assertCountEquals(0)
    }

    /**
     * Stored Lark data stays listed and readable because nothing that lists or opens it can see the flag: in the app's
     * sources `HubModule.Lark` is named only by Settings' [LarkBotEntry]. The Inbox, Messages, the readers and the
     * Lark connector keep following `LarkFeatures` alone.
     */
    @Test fun onlySettingsLarkBotEntryReadsTheModuleFlag() {
        val root = generateSequence(File("").absoluteFile) { it.parentFile }.first { File(it, "settings.gradle.kts").isFile }
        val readers = root.walkTopDown()
            .onEnter { it.name != "build" && it.name != ".gradle" }
            .filter { it.isFile && it.extension == "kt" && "${File.separator}src${File.separator}main${File.separator}" in it.path }
            .filter { "HubModule.Lark" in it.readText() }
            .map { it.relativeTo(root).invariantSeparatorsPath }.toList()
        assertEquals(listOf("feature/settings/src/main/kotlin/persona/feature/settings/LarkBotEntry.kt"), readers)
    }
}

/**
 * The same pages drawn on the JVM with Robolectric's native graphics (no emulator), in every style, light and dark, in
 * each language: Settings › Channels with the Lark module on and off, and the page a link to the bot lands on when it is
 * off. PNGs go to `build/lark-module-shots/` (or `-Dpersona.shots=<dir>`) to look at; none is committed.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class LarkBotModuleOffShotsTest {
    @get:Rule val rule = createComposeRule()

    private fun out(): File = File(System.getProperty("persona.shots") ?: "build/lark-module-shots").also { it.mkdirs() }

    private enum class Scene(val features: Set<String>, val page: String) {
        ChannelsOn(LarkOn, "channels"), ChannelsOff(LarkOff, "channels"), BotOff(LarkOff, "feishu-bot")
    }

    private fun shots(language: String) {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        var scene by mutableStateOf(Scene.ChannelsOn)
        var fontScale by mutableStateOf(1f)
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                val density = androidx.compose.ui.platform.LocalDensity.current
                CompositionLocalProvider(LocalHubFeatures provides scene.features,
                    androidx.compose.ui.platform.LocalDensity provides androidx.compose.ui.unit.Density(density.density, fontScale)) {
                    key(style, dark, scene, fontScale) {
                        Box(Modifier.testTag("shot").width(400.dp).height(900.dp).background(PersonaTheme.palette.background)) {
                            SettingsScreen(services(RecordingChannels(), RecordingBot()), initialPage = scene.page, onBack = {})
                        }
                    }
                }
            }
        }
        val offLine = ApplicationProvider.getApplicationContext<Context>().getString(R.string.settings_lark_bot_module_off)
        for (s in AppStyle.entries) for (d in listOf(false, true)) for (next in Scene.entries) {
            style = s; dark = d; scene = next
            rule.waitUntil(5_000) {
                rule.onAllNodesWithText(if (next == Scene.BotOff) offLine else "Example Chat").fetchSemanticsNodes().isNotEmpty()
            }
            if (next != Scene.BotOff) rule.onAllNodes(hasText("Lark bot") and hasClickAction()).assertCountEquals(if (next == Scene.ChannelsOn) 1 else 0)
            rule.waitForIdle()
            val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
            val name = next.name.replace(Regex("([a-z])([A-Z])"), "$1-$2").lowercase()
            File(out(), "lark-$name-$language-${s.name.lowercase()}-${if (d) "dark" else "light"}.png").outputStream()
                .use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
        }
        // The line at the largest text size: it wraps and is not cut off.
        style = AppStyle.Glass; dark = false; scene = Scene.BotOff; fontScale = 2f
        rule.waitUntil(5_000) { rule.onAllNodesWithText(offLine).fetchSemanticsNodes().isNotEmpty() }
        rule.waitForIdle()
        File(out(), "lark-bot-off-$language-glass-light-font2.png").outputStream()
            .use { rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap().compress(Bitmap.CompressFormat.PNG, 100, it) }
    }

    @Config(sdk = [35], qualifiers = "en-w400dp-h900dp-xhdpi") @Test fun english() = shots("en")
    @Config(sdk = [35], qualifiers = "zh-w400dp-h900dp-xhdpi") @Test fun chinese() = shots("zh")
    @Config(sdk = [35], qualifiers = "ja-w400dp-h900dp-xhdpi") @Test fun japanese() = shots("ja")
}
