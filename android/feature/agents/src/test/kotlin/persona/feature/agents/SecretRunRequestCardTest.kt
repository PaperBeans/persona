package persona.feature.agents

import androidx.compose.ui.autofill.ContentDataType
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsEnabled
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onFirst
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performImeAction
import androidx.compose.ui.test.performTextInput
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.Interaction
import persona.core.contracts.InteractionField
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaTheme
import persona.core.domain.answer.AnswerAction
import persona.core.domain.answer.AnswerDraft
import java.time.Instant

/**
 * The assistant's password card (`secret_run`, #1137, #1581) in the Agents tab: the computer and the commands, a
 * password field, Decline and Run, as in the chat. Run hands over the typed password once and empties the field.
 */
@RunWith(AndroidJUnit4::class)
// Tall enough for the whole card: taps land only inside the window.
@Config(sdk = [35], qualifiers = "zh-w400dp-h1200dp")
class SecretRunRequestCardTest {
    @get:Rule val rule = createComposeRule()

    private val now = Instant.parse("2026-11-06T08:00:00Z")
    private val commands = listOf("sudo -S -k -p '' launchctl bootstrap system /Library/LaunchDaemons/com.example.backup.plist", "sudo -S -k -p '' true")
    private val card = Interaction("ix_secret", machineId = "hub", sessionId = "chat", sessionTitle = "Persona", kind = "permission",
        toolName = "secret_run", expiresAt = now.plusSeconds(600).toString(),
        toolInput = buildJsonObject {
            put("machine", "macmini"); put("machine_name", "Mac mini"); put("purpose", "Install the launch daemon for the backup job")
            put("commands", JsonArray(commands.map { JsonPrimitive(it) })); put("delivery", "stdin")
        },
        fields = listOf(InteractionField(key = "password", label = "Password", secret = true)))
    private val envCommands = listOf("example auth login --password-stdin", "example verify  --json")
    private val envCard = card.copy(toolInput = buildJsonObject {
        put("machine", "macmini"); put("machine_name", "Mac mini"); put("purpose", "Authenticate the one-off example commands")
        put("commands", JsonArray(envCommands.map { JsonPrimitive(it) })); put("delivery", "env")
    })
    private val envWarningZh = "警告：上面列出的每个命令都会通过 PERSONA_SECRET 直接收到你输入的密码。"
    private val envWarningEn = "Warning: every listed command receives your typed password directly in PERSONA_SECRET."
    private val envWarningJa = "警告：上記の各コマンドは、入力したパスワードを PERSONA_SECRET 経由で直接受け取ります。"
    private val typed = "correct horse battery staple"
    private val answers = mutableListOf<Pair<AnswerAction, AnswerDraft>>()

    private fun field() = rule.onNode(hasSetTextAction())
    private fun typedLength() = field().fetchSemanticsNode().config[SemanticsProperties.EditableText].text.length

    private fun show(request: Interaction = card) = rule.setContent {
        PersonaTheme(style = AppStyle.Glass, darkTheme = false, reduceMotion = true) {
            RequestCard(request, "hub", AgentsMetadata(), now, online = true, delivery = null, onOpen = {},
                onAnswer = { _, action, draft, _ -> answers += action to draft })
        }
    }

    private fun assertEnvWarningAndCommands(warning: String) {
        rule.onAllNodes(hasText(warning, substring = true), useUnmergedTree = true).assertCountEquals(1)
        envCommands.forEach { rule.onNodeWithText(it, useUnmergedTree = true).assertExists() }
    }

    @Test fun showsTheCommandsAPasswordFieldDeclineAndRun() {
        show()
        rule.onAllNodesWithText("用你的密码在 Mac mini 上运行").onFirst().assertExists()
        rule.onAllNodesWithText("Install the launch daemon for the backup job").onFirst().assertExists()
        commands.forEach { rule.onNodeWithText(it, useUnmergedTree = true).assertExists() }
        rule.onAllNodesWithText("请在 iPhone 上填写").assertCountEquals(0)
        // No "允许" or "总是允许…", and not the generic fields note ("…才能允许，请到网页或 iPhone 上处理…").
        rule.onAllNodes(hasText("允许", substring = true), useUnmergedTree = true).assertCountEquals(0)
        rule.onAllNodes(hasText("PERSONA_SECRET", substring = true), useUnmergedTree = true).assertCountEquals(0)
        rule.onAllNodes(hasSetTextAction()).assertCountEquals(1)
        field().assert(SemanticsMatcher.keyIsDefined(SemanticsProperties.Password))
        field().assert(SemanticsMatcher.expectValue(SemanticsProperties.ContentDataType, ContentDataType.None))
        rule.onNodeWithText("运行").assertIsNotEnabled()
        rule.onNodeWithText("拒绝").assertIsEnabled().performClick()
        assertEquals(listOf(AnswerAction.Deny to AnswerDraft()), answers)
    }

    @Test fun envDeliveryWarnsThatEveryListedCommandReceivesThePassword() {
        show(envCard)
        assertEnvWarningAndCommands(envWarningZh)
    }

    @Test @Config(sdk = [35], qualifiers = "en-w400dp-h1200dp")
    fun envDeliveryWarningIsLocalizedInEnglish() {
        show(envCard)
        assertEnvWarningAndCommands(envWarningEn)
    }

    @Test @Config(sdk = [35], qualifiers = "ja-w400dp-h1200dp")
    fun envDeliveryWarningIsLocalizedInJapanese() {
        show(envCard)
        assertEnvWarningAndCommands(envWarningJa)
    }

    @Test fun runHandsOverTheTypedPasswordOnceAndEmptiesTheField() {
        show()
        field().performTextInput(typed)
        field().performImeAction()
        assertTrue(answers.isEmpty())
        rule.onNodeWithText("运行").assertIsEnabled().performClick()
        assertEquals(listOf(AnswerAction.Allow to AnswerDraft(password = typed)), answers)
        assertEquals(0, typedLength())
        rule.onNodeWithText("运行").assertIsNotEnabled().performClick()
        assertEquals(1, answers.size)
    }

    @Test fun envRunHandsOverOnlyTheFreshPasswordOnceAndEmptiesTheField() {
        show(envCard)
        field().performTextInput(typed)
        rule.onNodeWithText("运行").assertIsEnabled().performClick()
        assertEquals(listOf(AnswerAction.Allow to AnswerDraft(password = typed)), answers)
        assertEquals(0, typedLength())
        rule.onNodeWithText("运行").assertIsNotEnabled().performClick()
        assertEquals(1, answers.size)
    }

    @Test fun declineEmptiesTheField() {
        show()
        field().performTextInput(typed)
        rule.onNodeWithText("拒绝").performClick()
        assertEquals(listOf(AnswerAction.Deny to AnswerDraft()), answers)
        assertEquals(0, typedLength())
    }
}
