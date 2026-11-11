package persona.feature.settings

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.performScrollToNode
import androidx.compose.ui.test.performSemanticsAction
import androidx.compose.ui.test.hasScrollAction
import androidx.navigation3.runtime.NavKey
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.flowOf
import kotlinx.serialization.json.JsonObject
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.ServerEvent
import persona.core.contracts.Session
import persona.core.contracts.api.*
import persona.core.designsystem.LocalManagerRoster
import persona.core.designsystem.PersonaTheme
import persona.core.navigation.AssistantRoute
import persona.core.navigation.ChatRoute
import persona.core.navigation.ConnectorRoute
import persona.core.navigation.ExtensionCatalogRoute
import persona.core.navigation.ExtensionRoute
import persona.core.navigation.ModelPurposeRoute
import persona.core.navigation.SettingsPage
import persona.core.navigation.SettingsPageRoute
import persona.core.navigation.SettingsRoute

/**
 * With another assistant current, what a page of Settings' first group opens is that assistant's (docs/assistant/managers.md
 * "Clients"), not the default's: a market's channel installs for it, a channel's "More settings" opens its copy of the
 * extension, the Lark connector's agent model is its own. And the shell closes those pages on a switch.
 */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "zh-w400dp-h2000dp-xhdpi")
class SettingsAssistantEntriesTest {
    @get:Rule val rule = createComposeRule()
    @After fun reset() = AssistantManagerChoice.reset()

    private val roster = ManagerRoster(ManagersSample, "assistant")
    private val work = "m-3fa21c"
    private val opened = mutableListOf<NavKey>()

    private val channels = object : ChannelManagement {
        override val available = MutableStateFlow(true)
        override val conversations = flowOf(emptyList<Session>())
        private val list = listOf(
            HubChannel("mail-bot", "邮件机器人", "im", "plugin", extension = "mail-bot", installed = true, enabled = true, state = "connected"),
            HubChannel("market-chat", "市场里的渠道", "im", "market", extension = "chat-ext", market = "official", state = "not_installed"),
        )
        override suspend fun channels() = list
        override suspend fun channel(id: String) = ChannelDetail(list.first { it.id == id }, settingsUrl = "settings/plugins/mail-bot")
        override suspend fun update(id: String, change: ChannelChange) = channel(id)
        override suspend fun bind(id: String, step: String?, values: Map<String, String>?) = error("not used")
        override suspend fun unbind(id: String) = channel(id)
        override suspend fun test(id: String) {}
    }

    private val connectors = object : ConnectorsApiStub() {
        override suspend fun larkStatus() = LarkStatus()
        override suspend fun larkAccounts(): LarkAccounts? = null
    }

    private fun services() = SettingsServices(
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
            override val machines = MutableStateFlow(emptyList<persona.core.contracts.Machine>())
            override val sessions = MutableStateFlow(emptyList<Session>())
            override suspend fun refresh() {}
        },
        updates = object : AppUpdates { override suspend fun check(): AppUpdate? = null },
        managers = FakeManagers(),
        channels = channels,
        connectors = connectors,
    )

    private fun show(page: String) {
        AssistantManagerChoice.choose("hub", work, "assistant")
        val services = services()
        rule.setContent {
            PersonaTheme(reduceMotion = true) {
                CompositionLocalProvider(LocalManagerRoster provides roster) {
                    Box(Modifier.fillMaxSize()) { SettingsScreen(services, initialPage = page, onNavigate = { opened += it }) }
                }
            }
        }
        rule.waitForIdle()
    }

    private fun tap(text: String) {
        rule.onNode(hasScrollAction()).performScrollToNode(hasText(text))
        rule.onNode(hasText(text) and hasClickAction()).performSemanticsAction(SemanticsActions.OnClick)
        rule.waitForIdle()
    }

    @Test fun aMarketsChannelInstallsForTheCurrentAssistant() {
        show("channels")
        tap("市场里的渠道")
        assertEquals(ExtensionCatalogRoute("official", "chat-ext", manager = work), opened.last())
    }

    @Test fun aChannelsMoreSettingsOpenTheCurrentAssistantsExtension() {
        assertEquals(ExtensionRoute("mail-bot", manager = work), ChannelText.settingsDestination("settings/plugins/mail-bot", work))
        assertEquals(ExtensionRoute("mail-bot"), ChannelText.settingsDestination("settings/plugins/mail-bot"))
        AssistantManagerChoice.choose("hub", work, "assistant")
        rule.setContent {
            PersonaTheme(reduceMotion = true) {
                CompositionLocalProvider(LocalManagerRoster provides roster) {
                    Box(Modifier.fillMaxSize()) { ChannelPage(channels, "mail-bot", onBack = {}, onOpen = { opened += it }) }
                }
            }
        }
        rule.waitForIdle()
        tap("更多设置")
        assertEquals(ExtensionRoute("mail-bot", manager = work), opened.last())
    }

    @Test fun theLarkAgentsModelIsTheCurrentAssistants() {
        show("feishu")
        tap("/agent 模型")
        assertEquals(ModelPurposeRoute("lark.agent", work), opened.last())
    }

    /** The shell's own path (Root.kt): the sheet's stack and each tab's lose the first group's pages on a switch, not before. */
    @Test fun theShellClosesThemOnEveryStackOnASwitch() {
        val sheet = mutableStateListOf<NavKey>(SettingsRoute, SettingsPageRoute(SettingsPage.Connectors), ConnectorRoute("github", "assistant"))
        val tab = mutableStateListOf<NavKey>(AssistantRoute, ChatRoute("server", "s1"), SettingsPageRoute(SettingsPage.Mail))
        val device = mutableStateListOf<NavKey>(SettingsRoute, SettingsPageRoute(SettingsPage.Storage))
        var current by mutableStateOf("assistant")
        rule.setContent { CloseAssistantSettingsOnSwitch(current) { listOf(sheet, tab, device) } }
        rule.waitForIdle()
        assertEquals(3, sheet.size)
        assertEquals(3, tab.size)
        current = work
        rule.waitForIdle()
        assertEquals(listOf<NavKey>(SettingsRoute), sheet.toList())
        assertEquals(listOf<NavKey>(AssistantRoute, ChatRoute("server", "s1")), tab.toList())
        assertEquals(2, device.size)
    }
}
