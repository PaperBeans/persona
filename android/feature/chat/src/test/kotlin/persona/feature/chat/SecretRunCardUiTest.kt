package persona.feature.chat

import android.graphics.Bitmap
import android.content.res.Configuration
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.Modifier
import androidx.compose.ui.autofill.ContentDataType
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsEnabled
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.StateRestorationTester
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onFirst
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performImeAction
import androidx.compose.ui.test.performTextInput
import androidx.compose.ui.unit.Density
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleOwner
import androidx.lifecycle.LifecycleRegistry
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.test.ext.junit.runners.AndroidJUnit4
import coil3.ImageLoader
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.Interaction
import persona.core.contracts.InteractionField
import persona.core.contracts.Item
import persona.core.contracts.Resolution
import persona.core.contracts.SessionKey
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.Tokens
import persona.core.domain.lines.ChatLine
import persona.core.domain.lines.ToolCall
import persona.core.domain.lines.WorkItem
import java.io.File

/**
 * The assistant's password card (`secret_run`, #1137, #1581) in the chat: the computer, the purpose and every command
 * exactly as it will run, a password field, Decline and Run; no Allow, no Always allow, no hand back. The password lives
 * only in the card: Run sends it once and empties the field, and nothing restores it. Robolectric native captures of every
 * style in light and dark go to `build/secret-run-shots/` (not emulator screenshots).
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "zh-w400dp-h800dp-xhdpi", application = QuestionTestApplication::class)
class SecretRunCardUiTest {
    @get:Rule val rule = createComposeRule()

    private val commands = listOf(
        "sudo -S -k -p '' launchctl bootstrap system /Library/LaunchDaemons/com.example.backup.plist",
        "sudo -S -k -p '' sh -c \"echo  two  spaces > /tmp/x\"",
    )
    private val card = Interaction("ix_secret", machineId = "hub", sessionId = "chat", sessionTitle = "Persona", kind = "permission",
        toolName = "secret_run", summary = "Mac mini\nInstall the launch daemon for the backup job\n$ ${commands.first()}",
        toolInput = buildJsonObject {
            put("machine", "macmini"); put("machine_name", "Mac mini"); put("purpose", "Install the launch daemon for the backup job")
            put("commands", JsonArray(commands.map { JsonPrimitive(it) })); put("delivery", "stdin")
        },
        fields = listOf(InteractionField(key = "password", label = "Password", secret = true)))
    private val envCommands = listOf("example auth login --password-stdin", "example verify  --json")
    private val envCard = card.copy(
        summary = "Mac mini\nAuthenticate the one-off example commands\n${envCommands.joinToString("\n") { "$ $it" }}",
        toolInput = buildJsonObject {
            put("machine", "macmini"); put("machine_name", "Mac mini"); put("purpose", "Authenticate the one-off example commands")
            put("commands", JsonArray(envCommands.map { JsonPrimitive(it) })); put("delivery", "env")
        },
    )
    private val envWarningZh = "警告：上面列出的每个命令都会通过 PERSONA_SECRET 直接收到你输入的密码。"
    private val envWarningEn = "Warning: every listed command receives your typed password directly in PERSONA_SECRET."
    private val envWarningJa = "警告：上記の各コマンドは、入力したパスワードを PERSONA_SECRET 経由で直接受け取ります。"
    private val typed = "correct horse battery staple"
    private val sent = mutableListOf<Resolution>()

    private fun field() = rule.onNode(hasSetTextAction())
    /** How many characters the field holds (its semantics may carry the masked text, so only the length is compared). */
    private fun typedLength() = field().fetchSemanticsNode().config[SemanticsProperties.EditableText].text.length
    private fun runButton() = rule.onNodeWithText("运行")

    /** The chat's request dock with [requests]; every answer it sends lands in [sent], then [onSent] runs. */
    @Composable private fun Panel(requests: List<Interaction> = listOf(card), onSent: () -> Unit = {}) =
        InteractionPanel(requests, null, 800.dp, null, emptySet(), { _, resolution -> sent += resolution; onSent() }, null, {})

    private fun show(request: Interaction = card) = rule.setContent { PersonaTheme(reduceMotion = true) { Panel(listOf(request)) } }

    private fun assertEnvWarningAndCommands(warning: String) {
        rule.onAllNodes(hasText(warning, substring = true), useUnmergedTree = true).assertCountEquals(1)
        envCommands.forEach { rule.onNodeWithText(it, useUnmergedTree = true).assertExists() }
    }

    @Test fun showsWhatWouldRunWithAPasswordFieldDeclineAndRun() {
        show()
        rule.onAllNodesWithText("用你的密码在 Mac mini 上运行").onFirst().assertExists()
        rule.onAllNodesWithText("Install the launch daemon for the backup job").onFirst().assertExists()
        commands.forEach { rule.onNodeWithText(it, useUnmergedTree = true).assertExists() }
        rule.onAllNodesWithText("请在 iPhone 上填写").assertCountEquals(0)
        // Neither "允许" nor "总是允许…", no deny reason: the password field is the only thing to type into.
        rule.onAllNodes(hasText("允许", substring = true), useUnmergedTree = true).assertCountEquals(0)
        rule.onAllNodes(hasSetTextAction()).assertCountEquals(1)
        // Masked, and outside Autofill: never filled from, nor offered to, a password manager.
        field().assert(SemanticsMatcher.keyIsDefined(SemanticsProperties.Password))
        field().assert(SemanticsMatcher.expectValue(SemanticsProperties.ContentDataType, ContentDataType.None))
        rule.onNodeWithText("只用这一次，不会保存，助手也看不到。").assertExists()
        rule.onAllNodes(hasText("PERSONA_SECRET", substring = true), useUnmergedTree = true).assertCountEquals(0)
        runButton().assertIsNotEnabled().performClick()
        // Nothing to hand back to a computer, no terminal to explain: the card is the assistant's own.
        rule.onAllNodesWithText("交给电脑处理").assertCountEquals(0)
        rule.onAllNodesWithText("终端处理说明").assertCountEquals(0)
        assertTrue(sent.isEmpty())
    }

    @Test fun envDeliveryWarnsThatEveryListedCommandReceivesThePassword() {
        show(envCard)
        assertEnvWarningAndCommands(envWarningZh)
    }

    @Test @Config(sdk = [35], qualifiers = "en-w400dp-h1000dp-xhdpi", application = QuestionTestApplication::class)
    fun envDeliveryWarningIsLocalizedInEnglish() {
        show(envCard)
        assertEnvWarningAndCommands(envWarningEn)
    }

    @Test @Config(sdk = [35], qualifiers = "ja-w400dp-h1000dp-xhdpi", application = QuestionTestApplication::class)
    fun envDeliveryWarningIsLocalizedInJapanese() {
        show(envCard)
        assertEnvWarningAndCommands(envWarningJa)
    }

    @Test fun runSendsTheTypedPasswordOnceAndEmptiesTheField() {
        show()
        field().performTextInput(typed)
        assertEquals(typed.length, typedLength())
        runButton().assertIsEnabled().performClick()
        assertEquals(listOf(Resolution(allow = true, values = mapOf("password" to typed))), sent)
        assertEquals(0, typedLength())
        runButton().assertIsNotEnabled().performClick()
        assertEquals(1, sent.size)
    }

    @Test fun envRunSendsOnlyTheFreshPasswordOnceAndEmptiesTheField() {
        show(envCard)
        field().performTextInput(typed)
        runButton().assertIsEnabled().performClick()
        assertEquals(listOf(Resolution(allow = true, values = mapOf("password" to typed))), sent)
        assertEquals(0, typedLength())
        runButton().assertIsNotEnabled().performClick()
        assertEquals(1, sent.size)
    }

    /** A pasted line break (hidden behind the mask) never gets in: the hub would refuse the password and run nothing. */
    @Test fun aPastedLineBreakNeverGetsIn() {
        show()
        field().performTextInput("$typed\n")
        assertEquals(typed.length, typedLength())
        runButton().performClick()
        assertEquals(listOf(Resolution(allow = true, values = mapOf("password" to typed))), sent)
    }

    /** Without readable commands the card shows its summary (which repeats every command), never Run under nothing. */
    @Test fun withoutCommandsTheSummaryStandsInForThem() {
        rule.setContent { PersonaTheme(reduceMotion = true) { Panel(listOf(card.copy(toolInput = buildJsonObject { put("machine", "macmini") }))) } }
        rule.onAllNodesWithText(card.summary!!, useUnmergedTree = true).onFirst().assertExists()
        rule.onAllNodesWithText("依次运行的命令").assertCountEquals(0)
    }

    /** As on iOS, the keyboard's action key only puts the keyboard away: the commands run on a tap on Run. */
    @Test fun theKeyboardActionDoesNotRun() {
        show()
        field().performTextInput(typed)
        field().performImeAction()
        assertTrue(sent.isEmpty())
        assertEquals(typed.length, typedLength())
    }

    @Test fun declineSendsAPlainDenyAndEmptiesTheField() {
        show()
        field().performTextInput(typed)
        rule.onNodeWithText("拒绝").assertIsEnabled().performClick()
        assertEquals(listOf(Resolution(allow = false)), sent)
        assertEquals(0, typedLength())
    }

    /** Another card in its place, the card leaving the screen and the app going to the background all start it empty. */
    @Test fun anotherCardLeavingAndTheBackgroundEmptyIt() {
        val other = card.copy(id = "ix_secret_2")
        var requests by mutableStateOf(listOf(card))
        var shown by mutableStateOf(true)
        val owner = object : LifecycleOwner {
            val registry = LifecycleRegistry.createUnsafe(this).apply { currentState = Lifecycle.State.RESUMED }
            override val lifecycle: Lifecycle get() = registry
        }
        rule.setContent {
            CompositionLocalProvider(LocalLifecycleOwner provides owner) {
                PersonaTheme(reduceMotion = true) {
                    if (shown) Panel(requests)
                }
            }
        }
        field().performTextInput(typed)
        requests = listOf(other)
        rule.waitForIdle()
        assertEquals(0, typedLength())

        field().performTextInput(typed)
        shown = false
        rule.waitForIdle()
        shown = true
        rule.waitForIdle()
        assertEquals(0, typedLength())

        field().performTextInput(typed)
        rule.runOnIdle { owner.registry.currentState = Lifecycle.State.CREATED }
        rule.waitForIdle()
        assertEquals(0, typedLength())
        assertTrue(sent.isEmpty())
    }

    /** Recreating the activity (saved instance state) never brings a typed password back. */
    @Test fun thePasswordIsNotRestoredFromSavedState() {
        val restoration = StateRestorationTester(rule)
        restoration.setContent {
            PersonaTheme(reduceMotion = true) {
                Panel()
            }
        }
        field().performTextInput(typed)
        restoration.emulateSavedInstanceStateRestore()
        assertEquals(0, typedLength())
        runButton().assertIsNotEnabled()
        assertTrue(sent.isEmpty())
    }

    @Test fun drawsEnvWarningInLightAndDarkAtDefaultAndLargestText() {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        var large by mutableStateOf(false)
        rule.setContent {
            val scale = if (large) 2f else 1f
            val configuration = Configuration(LocalConfiguration.current).apply { fontScale = scale }
            CompositionLocalProvider(
                LocalConfiguration provides configuration,
                LocalDensity provides Density(LocalDensity.current.density, scale),
            ) {
                PersonaTheme(style = style, darkTheme = dark, reduceMotion = true) {
                    Column(Modifier.width(400.dp).background(PersonaTheme.palette.background).testTag("secret-run-env-shot")) {
                        Panel(listOf(envCard))
                    }
                }
            }
        }
        val output = File("build/secret-run-shots").also { it.mkdirs() }
        for (s in AppStyle.entries) {
            style = s
            for (big in listOf(false, true)) {
                val pixels = mutableListOf<IntArray>()
                for (d in listOf(false, true)) {
                    large = big; dark = d
                    Snapshot.sendApplyNotifications(); rule.waitForIdle()
                    assertEnvWarningAndCommands(envWarningZh)
                    val bitmap = rule.onNodeWithTag("secret-run-env-shot").captureToImage().asAndroidBitmap()
                    val font = if (big) "largest-text" else "default-text"
                    val appearance = if (d) "dark" else "light"
                    File(output, "env-${s.name.lowercase()}-$font-$appearance.png").outputStream().use {
                        assertTrue(bitmap.compress(Bitmap.CompressFormat.PNG, 100, it))
                    }
                    pixels += IntArray(bitmap.width * bitmap.height).also {
                        bitmap.getPixels(it, 0, bitmap.width, 0, 0, bitmap.width, bitmap.height)
                    }
                }
                assertFalse("env ${s.name} ${if (big) "largest" else "default"} text: light and dark must differ", pixels[0].contentEquals(pixels[1]))
            }
        }
    }

    /** The finished run as the chat shows it today: the assistant's secret_run step with its result. */
    private val ranStep: ChatLine.Step = run {
        val use = Item("tu_secret", kind = "tool_use", role = "assistant", toolName = "mcp__persona__secret_run", toolUseId = "tu_secret",
            toolInput = card.toolInput).asLineItem()
        val result = Item("tu_secret-r", kind = "tool_result", role = "user", toolUseId = "tu_secret",
            text = """{"status":"ran","machine":"Mac mini","message":"Ran 2 commands.","results":[""" +
                """{"command":"${commands[0].replace("\"", "\\\"")}","exit_code":0,"output":"","timed_out":false,"truncated":false,"output_withheld":false},""" +
                """{"command":"${commands[1].replace("\"", "\\\"")}","exit_code":0,"output":"","timed_out":false,"truncated":false,"output_withheld":false}]}""",
        ).asLineItem()
        ChatLine.Step("w-secret", WorkItem.Tool(ToolCall(use, result, emptyList())), true)
    }

    @Test fun drawsBeforeFilledAndAfterInEveryStyleLightAndDark() {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        var ran by mutableStateOf(false)
        rule.setContent {
            PersonaTheme(style = style, darkTheme = dark, reduceMotion = true) {
                Column(Modifier.width(400.dp).background(PersonaTheme.palette.background).testTag("secret-run-shot")) {
                    if (!ran) Panel { ran = true }
                    else {
                        val context = LocalContext.current
                        val loader = remember { ImageLoader.Builder(context).build() }
                        Column(Modifier.padding(Tokens.Space.l)) {
                            ChatLineContent(ranStep, SessionKey("hub", "chat"), loader, recordedDuration = null, onToggle = {}, onFold = {},
                                folding = emptySet(), foldErrors = emptyMap(), onFullText = {}, style = ChatRenderStyle.Assistant)
                        }
                    }
                }
            }
        }
        val output = File("build/secret-run-shots").also { it.mkdirs() }
        // A new style or appearance can rebuild the card, which empties the password: [prepare] fills it in again.
        fun capture(state: String, prepare: () -> Unit = {}) {
            for (s in AppStyle.entries) {
                val pixels = mutableListOf<IntArray>()
                for (d in listOf(false, true)) {
                    style = s; dark = d
                    Snapshot.sendApplyNotifications(); rule.waitForIdle()
                    prepare(); rule.waitForIdle()
                    val bitmap = rule.onNodeWithTag("secret-run-shot").captureToImage().asAndroidBitmap()
                    File(output, "$state-${s.name.lowercase()}-${if (d) "dark" else "light"}.png").outputStream().use {
                        assertTrue(bitmap.compress(Bitmap.CompressFormat.PNG, 100, it))
                    }
                    pixels += IntArray(bitmap.width * bitmap.height).also { bitmap.getPixels(it, 0, bitmap.width, 0, 0, bitmap.width, bitmap.height) }
                }
                assertFalse("$state ${s.name}: light and dark must differ", pixels[0].contentEquals(pixels[1]))
            }
        }
        runButton().assertIsNotEnabled()
        capture("before")
        capture("filled") { if (typedLength() == 0) field().performTextInput(typed) }
        runButton().assertIsEnabled().performClick()
        assertEquals(listOf(Resolution(allow = true, values = mapOf("password" to typed))), sent)
        capture("after")
    }
}
