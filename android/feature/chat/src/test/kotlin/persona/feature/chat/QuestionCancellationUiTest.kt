package persona.feature.chat

import android.content.Context
import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.unit.dp
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.*
import persona.core.designsystem.*
import persona.core.domain.answer.*
import java.io.File

@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h800dp-xhdpi", application = QuestionTestApplication::class)
class QuestionCancellationUiTest {
    @get:Rule val rule = createComposeRule()
    private val question = Interaction("q", kind = "question", canCancel = true,
        questions = listOf(Question("Choose a format", options = listOf(QuestionOption("JSON"), QuestionOption("CSV")))))
    private val close = "Close question without answering"
    private val triageQuestion = question.copy(
        id = "work_pending", toolName = "persona_triage", sessionTitle = "App verification",
        questions = listOf(Question("手机准备好验收了吗？", options = listOf(
            QuestionOption("准备好了，开始验收"), QuestionOption("先不管，稍后再验收"),
        ))),
    )

    @Test fun triageCloseDiscardsOnlyCurrentCardAndLaterQuestionStillAnswers() {
        var requests by mutableStateOf(listOf(triageQuestion))
        val sent = mutableListOf<Pair<String, Resolution>>()
        rule.setContent {
            PersonaTheme(reduceMotion = true) {
                InteractionPanel(requests, null, 600.dp, null, emptySet(), { ix, resolution ->
                    sent += ix.id to resolution; requests = emptyList()
                }, null, {})
            }
        }
        rule.onAllNodesWithText("手机准备好验收了吗？").onFirst().assertIsDisplayed()
        rule.onNodeWithContentDescription(close).assertIsEnabled().performClick()
        assertEquals(listOf(triageQuestion.id to Resolution(cancel = true)), sent)
        rule.onNodeWithContentDescription(close).assertDoesNotExist()
        requests = listOf(question.copy(id = "work_new", toolName = "persona_triage"))
        rule.onAllNodesWithText("Choose a format").onFirst().assertIsDisplayed()
        rule.onNodeWithContentDescription(close).assertIsEnabled()
        rule.onNodeWithText("1. JSON").performClick()
        assertEquals("work_new" to Resolution(answers = mapOf("Choose a format" to "JSON")), sent.last())
        assertEquals(2, sent.size)
    }

    @Test fun cardInViewStaysWhenAnEarlierCardLeaves() {
        fun card(id: String, text: String) = question.copy(id = id,
            questions = listOf(Question(text, options = listOf(QuestionOption("JSON"), QuestionOption("CSV")))))
        val first = card("first", "First card?")
        val second = card("second", "Second card?")
        val third = card("third", "Third card?")
        var requests by mutableStateOf(listOf(first, second, third))
        val sent = mutableListOf<String>()
        rule.setContent {
            PersonaTheme(reduceMotion = true) {
                InteractionPanel(requests, null, 600.dp, null, emptySet(), { ix, _ -> sent += ix.id }, null, {})
            }
        }
        rule.onNodeWithText("Next").performClick()
        rule.onAllNodesWithText("Second card?").onFirst().assertIsDisplayed()
        // answered on another device: the card in view stays, it does not hand its place to the third
        requests = listOf(second, third)
        rule.onAllNodesWithText("Second card?").onFirst().assertIsDisplayed()
        rule.onAllNodesWithText("Third card?").assertCountEquals(0)
        rule.onNodeWithText("1 / 2").assertIsDisplayed()
        // a card sorted in front of it does not take its place either
        requests = listOf(first, second, third)
        rule.onAllNodesWithText("Second card?").onFirst().assertIsDisplayed()
        rule.onNodeWithText("2 / 3").assertIsDisplayed()
        rule.onNodeWithText("1. JSON").performClick()
        assertEquals(listOf("second"), sent)
        // once it leaves, the card that took its place is shown
        requests = listOf(first, third)
        rule.onAllNodesWithText("Third card?").onFirst().assertIsDisplayed()
        rule.onNodeWithText("2 / 2").assertIsDisplayed()
        assertEquals(1, requestIndex(listOf(first, third), "second", 1))
        assertEquals(0, requestIndex(listOf(third), "second", 1))
        assertEquals(1, requestIndex(listOf(first, second), "second", 0))
    }

    @Test fun triageCollapseDoesNotSubmitAndLaterKeepsOriginalAnswerForServerIntent() {
        var collapsed by mutableStateOf(false)
        val sent = mutableListOf<Resolution>()
        rule.setContent {
            PersonaTheme(reduceMotion = true) {
                InteractionPanel(listOf(triageQuestion), null, 600.dp, null, emptySet(), { _, resolution -> sent += resolution },
                    null, {}, collapsed = collapsed, onCollapsedChange = { collapsed = it })
            }
        }
        rule.onNodeWithContentDescription("Collapse").performClick()
        assertTrue(sent.isEmpty())
        rule.onNodeWithContentDescription(close).assertIsEnabled()
        rule.onNodeWithContentDescription("Expand").performClick()
        rule.onNodeWithText("2. 先不管，稍后再验收").performClick()
        assertEquals(listOf(Resolution(answers = mapOf("手机准备好验收了吗？" to "先不管，稍后再验收"))), sent)
    }

    @Test fun triageScreenshotsAcrossStyles() {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        rule.setContent {
            PersonaTheme(style = style, darkTheme = dark, reduceMotion = true) {
                Column(Modifier.width(400.dp).background(PersonaTheme.palette.background).testTag("triage-shot")) {
                    InteractionPanel(listOf(triageQuestion), null, 600.dp, null, emptySet(), { _, _ -> }, null, {})
                }
            }
        }
        val output = File("build/question-cancel-shots/triage").also { it.mkdirs() }
        for (s in AppStyle.entries) for (d in listOf(false, true)) {
            style = s; dark = d; rule.waitForIdle()
            rule.onNodeWithContentDescription(close).assertIsDisplayed().assertIsEnabled()
            rule.onAllNodesWithText("手机准备好验收了吗？").onFirst().assertIsDisplayed()
            val bitmap = rule.onNodeWithTag("triage-shot").captureToImage().asAndroidBitmap()
            File(output, "${s.name.lowercase()}-${if (d) "dark" else "light"}.png").outputStream().use {
                assertTrue(bitmap.compress(Bitmap.CompressFormat.PNG, 100, it))
            }
        }
    }

    @Test fun closeWithdrawsOnlySelectedQuestionAndCannotSubmitTwice() {
        var requests by mutableStateOf(listOf(question))
        val sent = mutableListOf<Resolution>()
        var busy by mutableStateOf<String?>(null)
        rule.setContent {
            PersonaTheme(reduceMotion = true) {
                InteractionPanel(requests, null, 500.dp, busy, emptySet(), { ix, resolution ->
                    sent += resolution; busy = ix.id
                }, null, {}, collapsed = true)
            }
        }
        rule.onNodeWithContentDescription(close).performClick()
        rule.onNodeWithContentDescription(close).assertIsNotEnabled().performClick()
        assertEquals(listOf(Resolution(cancel = true)), sent)
        requests = emptyList()
        rule.onNodeWithContentDescription(close).assertDoesNotExist()
    }

    @Test fun oldRuntimeAndApprovalNeverShowCloseAndBlockedRequestCannotCancel() {
        var request by mutableStateOf(question.copy(canCancel = null))
        var blocked by mutableStateOf<String?>(null)
        var uncertain by mutableStateOf(emptySet<String>())
        rule.setContent {
            PersonaTheme(reduceMotion = true) {
                InteractionPanel(listOf(request), null, 500.dp, null, uncertain,
                    { _, _ -> fail("Must not resolve") }, null, {}, collapsed = true, blockedReason = blocked)
            }
        }
        rule.onNodeWithContentDescription(close).assertDoesNotExist()
        request = question.copy(kind = "permission")
        rule.onNodeWithContentDescription(close).assertDoesNotExist()
        request = question; blocked = "Read only"
        rule.onNodeWithContentDescription("Dismiss card on this device").assertIsNotEnabled()
        blocked = null; uncertain = setOf(question.id)
        rule.onNodeWithContentDescription(close).assertIsNotEnabled()
        uncertain = emptySet(); request = question.copy(expiresAt = "2000-01-01T00:00:00Z")
        rule.onNodeWithContentDescription(close).assertIsNotEnabled()
    }

    @Test fun collapseDoesNotAnswerAndNormalOptionStillAnswers() {
        var collapsed by mutableStateOf(false)
        val sent = mutableListOf<Resolution>()
        rule.setContent {
            PersonaTheme(reduceMotion = true) {
                InteractionPanel(listOf(question), null, 600.dp, null, emptySet(), { _, resolution -> sent += resolution },
                    null, {}, collapsed = collapsed, onCollapsedChange = { collapsed = it })
            }
        }
        rule.onNodeWithContentDescription("Collapse").performClick()
        assertTrue(sent.isEmpty())
        rule.onNodeWithContentDescription("Expand").performClick()
        rule.onNodeWithText("1. JSON").performClick()
        assertEquals(listOf(Resolution(answers = mapOf("Choose a format" to "JSON"))), sent)
    }

    @Test fun fullSheetCloseDiscardsQuestionWithoutFoldingOrSelecting() {
        var requests by mutableStateOf(listOf(question))
        val sent = mutableListOf<Resolution>()
        rule.setContent {
            PersonaTheme(reduceMotion = true) {
                InteractionPanel(requests, null, 600.dp, null, emptySet(), { _, resolution ->
                    sent += resolution; requests = emptyList()
                }, null, {})
            }
        }
        val resources = ApplicationProvider.getApplicationContext<Context>().resources
        rule.onNodeWithContentDescription(resources.getString(persona.core.designsystem.R.string.designsystem_decision_open_full)).performClick()
        rule.onNode(hasContentDescription(close) and hasAnyAncestor(isDialog())).performClick()
        assertEquals(listOf(Resolution(cancel = true)), sent)
        rule.onNodeWithContentDescription(close).assertDoesNotExist()
    }

    @Test fun screenshotsAcrossStylesAndReceiptNeverClaimsAnAnswer() {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        rule.setContent {
            PersonaTheme(style = style, darkTheme = dark, reduceMotion = true) {
                Column(Modifier.width(400.dp).background(PersonaTheme.palette.background).testTag("shot")) {
                    InteractionPanel(listOf(question), null, 600.dp, null, emptySet(), { _, _ -> }, null, {})
                }
            }
        }
        val output = File("build/question-cancel-shots").also { it.mkdirs() }
        for (s in AppStyle.entries) for (d in listOf(false, true)) {
            style = s; dark = d; rule.waitForIdle()
            val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
            File(output, "${s.name.lowercase()}-${if (d) "dark" else "light"}.png").outputStream().use {
                assertTrue(bitmap.compress(Bitmap.CompressFormat.PNG, 100, it))
            }
        }
        val res = ApplicationProvider.getApplicationContext<Context>().resources
        val reply = InteractionReply(InteractionSubmission.Answered)
        assertEquals("Question closed without an answer", reply.notice(res, question, Resolution(cancel = true)))
        assertFalse(reply.offersNext(Resolution(cancel = true)))
        assertFalse(InteractionReply(InteractionSubmission.AlreadyAnswered, Resolution(cancel = true)).offersNext(Resolution()))
    }
}
