package persona.feature.chat

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.click
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performImeAction
import androidx.compose.ui.test.performTextInput
import androidx.compose.ui.test.performTouchInput
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.Interaction
import persona.core.contracts.JsonCodec
import persona.core.contracts.Resolution
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaTheme
import java.io.File

@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en-w400dp-h900dp-xhdpi", application = QuestionTestApplication::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class CommandInputCardUiTest {
    @get:Rule val rule = createComposeRule()
    private var style by mutableStateOf(AppStyle.Glass)
    private var dark by mutableStateOf(false)
    private var refusal by mutableStateOf<String?>(null)
    private val replies = mutableListOf<Resolution>()

    private fun line() = rule.onNode(
        hasSetTextAction() and (hasTestTag("interaction.field.input") or hasAnyAncestor(hasTestTag("interaction.field.input"))),
        useUnmergedTree = true,
    )

    private fun show(masked: Boolean = true, dockHeight: Dp = 820.dp) {
        val maskField = if (masked) ""","masked":true""" else ""
        val card = JsonCodec.decodeFromString<Interaction>(
            """{"id":"fixture-command-input","machine_id":"example","session_id":"sample","kind":"permission",
              "tool_name":"command_input","tool_title":"Command input",
              "tool_input":{"command":"example-cli login","prompt":"Enter a test code","output":"Open https://example.com/device"$maskField},
              "fields":[{"key":"input","label":"Enter a test code","secret":true}]}""",
        )
        rule.setContent {
            PersonaTheme(style = style, darkTheme = dark, reduceMotion = true) {
                Column(Modifier.fillMaxWidth().background(PersonaTheme.palette.background)) {
                    InteractionPanel(listOf(card), null, dockHeight, null, emptySet(),
                        { _, resolution -> replies += resolution }, null, {}, terminalHelp = false,
                        commandInputErrors = refusal?.let { mapOf(card.id to it) }.orEmpty())
                }
            }
        }
    }

    @Test fun explicitSendSendsOnceAndRefusalLeavesAnEmptyCard() {
        show()
        val line = line()
        val password = SemanticsMatcher.keyIsDefined(SemanticsProperties.Password)
        val toggle = rule.onNodeWithTag("interaction.commandInput.visibility", useUnmergedTree = true)
        val send = rule.onNodeWithText("Send")
        send.assertIsNotEnabled()
        line.assert(password)
        line.performTextInput("example-code")
        toggle.performTouchInput { click() }
        rule.onNodeWithText("Hide", useUnmergedTree = true).assertIsDisplayed()
        line.assert(!password)
        assertEquals("example-code", line.fetchSemanticsNode().config[SemanticsProperties.EditableText].text)
        toggle.performTouchInput { click() }
        rule.onNodeWithText("Show", useUnmergedTree = true).assertIsDisplayed()
        line.assert(password)
        line.performImeAction()
        assertEquals(emptyList<Resolution>(), replies)
        send.performClick()
        assertEquals(listOf(Resolution(allow = true, values = mapOf("input" to "example-code"))), replies)
        send.assertIsNotEnabled()
        rule.runOnIdle { refusal = "The command refused the test line." }
        rule.onNodeWithText("The command refused the test line.", useUnmergedTree = true).assertIsDisplayed()
    }

    @Test fun unmaskedPromptStartsVisibleAndDecliningTypesNothing() {
        show(masked = false)
        val line = line()
        line.assert(!SemanticsMatcher.keyIsDefined(SemanticsProperties.Password))
        line.performTextInput("example-response")
        rule.onNodeWithText("Not now").performClick()
        assertEquals(listOf(Resolution(allow = false)), replies)
        rule.onNodeWithText("Send").assertIsNotEnabled()
    }

    @Test fun compactDockKeepsTheDecisionButtonsVisible() {
        show(dockHeight = 260.dp)
        rule.onNodeWithText("Not now").assertIsDisplayed()
        rule.onNodeWithText("Send").assertIsDisplayed().assertIsNotEnabled()
        rule.onNodeWithText("Not now").performClick()
        assertEquals(listOf(Resolution(allow = false)), replies)
    }

    @Test fun capturesAllThreeStylesInBothAppearances() {
        show()
        val destination = File(System.getProperty("persona.shots") ?: "build/command-input-shots").also { it.mkdirs() }
        for (appearance in listOf(false, true)) for (current in AppStyle.entries) {
            rule.runOnIdle { style = current; dark = appearance }
            rule.onNodeWithText("Waiting for your input", useUnmergedTree = true).assertIsDisplayed()
            rule.onNodeWithText("example-cli login", useUnmergedTree = true).assertIsDisplayed()
            rule.onNodeWithText("Open https://example.com/device", useUnmergedTree = true).assertIsDisplayed()
            val suffix = if (appearance) "dark" else "light"
            val styleName = current.name.lowercase()
            val bitmap = rule.onRoot().captureToImage().asAndroidBitmap()
            File(destination, "command-input-$styleName-$suffix.png").outputStream().use {
                check(bitmap.compress(Bitmap.CompressFormat.PNG, 100, it))
            }
        }
    }
}
