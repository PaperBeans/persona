package persona.feature.chat

import androidx.compose.runtime.collectAsState
import androidx.compose.ui.test.assertIsEnabled
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.MutableStateFlow
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.DelegatedAnswer
import persona.core.contracts.Interaction
import persona.core.contracts.Item
import persona.core.contracts.Machine
import persona.core.contracts.Question
import persona.core.contracts.QuestionOption
import persona.core.contracts.Resolution
import persona.core.contracts.Session
import persona.core.contracts.SessionKey
import persona.core.contracts.api.ChatRepository
import persona.core.contracts.api.ChatSnapshot
import persona.core.contracts.api.HubCodedFailure
import persona.core.contracts.api.InteractionCenter
import persona.core.contracts.api.MessageSender
import persona.core.contracts.api.OutboxMessage
import persona.core.contracts.api.SessionsRepository
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaTheme

/**
 * The question the assistant answered last, over an Agent chat's composer (#4339 C9, iOS `DelegatedCard`): which one,
 * where it shows, × and Change answer, and the words for a change that did not go (iOS `Delegated.reanswerError`).
 */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en-w400dp-h900dp")
class DelegatedCardTest {
    @get:Rule val rule = createComposeRule()

    private val key = SessionKey("mac", "a")
    private val driver = "Which driver?"
    private fun card(id: String, session: String = "a") = Interaction(id, "mac", session, kind = "question", manager = "work", questions = listOf(
        Question(driver, options = listOf(QuestionOption("pgx"), QuestionOption("sqlc")))))
    private fun answered(id: String, session: String = "a", answer: String = "pgx") =
        DelegatedAnswer(card(id, session), mapOf(driver to answer), "Because $id", null)

    private class Coded(override val status: Int?, override val code: String? = null, message: String = "") : Exception(message), HubCodedFailure

    private class Center(answers: List<DelegatedAnswer>) : InteractionCenter {
        override val pending = MutableStateFlow(emptyList<Interaction>())
        override suspend fun refresh() = Unit
        override suspend fun resolve(id: String, resolution: Resolution) = Unit
        override val delegated = MutableStateFlow(answers)
        override val reanswered = MutableStateFlow(emptyMap<String, Map<String, String>>())
        val sent = mutableListOf<Triple<String, Map<String, String>, String?>>()
        var refusal: Exception? = null
        override fun dropDelegated(id: String) { delegated.value = delegated.value.filterNot { it.id == id } }
        override suspend fun reanswer(id: String, answers: Map<String, String>, manager: String?) {
            refusal?.let { throw it }
            sent += Triple(id, answers, manager)
            reanswered.value = reanswered.value + (id to answers)
        }
    }

    private fun show(center: Center, canChange: Boolean = true) = rule.setContent {
        PersonaTheme(darkTheme = false, reduceMotion = true, style = AppStyle.Glass) { DelegatedCard(key, center, canChange) }
    }

    @Test fun theLatestAnswerForThisChatShowsUntilClosed() {
        val center = Center(listOf(answered("old", answer = "sqlc"), answered("other", session = "b"), answered("new")))
        show(center)
        rule.onNodeWithText("Answered by the assistant").assertExists()
        rule.onNodeWithText("Why: Because new").assertExists()
        rule.onNodeWithText("Why: Because other").assertDoesNotExist()
        rule.onNodeWithText("Why: Because old").assertDoesNotExist()
        // × puts it away: the one before it for this chat shows, until it is put away too.
        rule.onNodeWithContentDescription("Close").performClick()
        rule.onNodeWithText("Why: Because old").assertExists()
        rule.onNodeWithContentDescription("Close").performClick()
        rule.onNodeWithText("Answered by the assistant").assertDoesNotExist()
        assertEquals(listOf("other"), center.delegated.value.map { it.id })
    }

    @Test fun changeAnswerTakesWhatSendingDoes() {
        show(Center(listOf(answered("new"))), canChange = false)
        rule.onNodeWithText("Why: Because new").assertExists()
        rule.onNodeWithText("Change answer").assertDoesNotExist()
    }

    @Test fun changeAnswerSendsOnlyWhatChangedAndShowsIt() {
        val center = Center(listOf(answered("new")))
        show(center)
        rule.onNodeWithText("Change answer").performClick()
        rule.onNodeWithText("Other answer…").assertExists()
        // Nothing changed yet: nothing to send.
        rule.onNode(hasText("Send") and hasClickAction()).assertIsNotEnabled()
        rule.onNodeWithText("sqlc").performClick()
        rule.onNode(hasText("Send") and hasClickAction()).assertIsEnabled().performClick()
        rule.waitUntil(5_000) { center.sent.isNotEmpty() }
        assertEquals(listOf(Triple("new", mapOf(driver to "sqlc"), "work")), center.sent)
        rule.waitForIdle()
        rule.onNodeWithText("Other answer…").assertDoesNotExist()
        // The card says the answer now, what the assistant answered and that the user changed it.
        rule.onNodeWithText("sqlc").assertExists()
        rule.onNodeWithText("The assistant answered: pgx").assertExists()
        rule.onNodeWithText("You changed it").assertExists()
    }

    @Test fun aRefusedChangeSaysWhyAndStays() {
        val center = Center(listOf(answered("new"))).apply { refusal = Coded(503, "assistant_offline", "assistant offline") }
        show(center)
        rule.onNodeWithText("Change answer").performClick()
        rule.onNodeWithText("sqlc").performClick()
        rule.onNode(hasText("Send") and hasClickAction()).performClick()
        rule.waitUntil(5_000) { rule.onAllNodes(hasText("The assistant is offline. Try again shortly")).fetchSemanticsNodes().isNotEmpty() }
        rule.onNodeWithText("Other answer…").assertExists()
        assertTrue(center.reanswered.value.isEmpty())
    }

    @Test fun aChangeThatDidNotGoSaysWhy() {
        val res = ApplicationProvider.getApplicationContext<android.content.Context>().resources
        assertEquals("This device can only answer with a card's own choices, so it can't change answers", reanswerError(res, Coded(403)))
        assertEquals("The assistant didn't answer this question, so its answer can't be changed here", reanswerError(res, Coded(409)))
        assertEquals("Choose an answer first", reanswerError(res, Coded(400)))
        assertEquals("Couldn't send the new answer to the agent: node unreachable", reanswerError(res, Coded(502, message = "node unreachable")))
        assertEquals("The assistant is offline. Try again shortly", reanswerError(res, Coded(503, "assistant_offline")))
        assertEquals("This hub can't change answers yet. Update the hub first", reanswerError(res, Coded(404, message = "404 page not found")))
        assertEquals("Couldn't change the answer: gone", reanswerError(res, Coded(404, "not_found", "gone")))
        assertEquals("Couldn't change the answer: boom", reanswerError(res, Coded(500, message = "boom")))
        assertEquals("Can't reach the hub. That action didn't go through.", reanswerError(res, UnsupportedOperationException()))
    }

    /** Only Glass draws it, over an Agent chat's composer; Brief and Board stay as they were. */
    @Test fun onlyGlassAgentChatsDrawTheCard() {
        val row = Session("mac", "a", title = "Importer", status = "idle", agent = "claude", paneId = "p1")
        val chats = object : ChatRepository {
            override fun observe(session: SessionKey) = MutableStateFlow(ChatSnapshot(items = listOf(
                Item("u1", 1, role = "user", kind = "text", text = "Move the importer"),
                Item("a2", 2, role = "assistant", kind = "text", text = "Rewriting the queries"),
            ), loading = false))
            override suspend fun loadOlder(session: SessionKey) = Unit
            override suspend fun expandFold(session: SessionKey, fromSeq: Int, toSeq: Int): List<Item> = emptyList()
        }
        val sessions = object : SessionsRepository {
            override val machines = MutableStateFlow(listOf(Machine("mac", "Mac", online = true)))
            override val sessions = MutableStateFlow(listOf(row))
            override suspend fun refresh() = Unit
        }
        val sender = object : MessageSender {
            override val outbox = MutableStateFlow(emptyList<OutboxMessage>())
            override suspend fun send(session: SessionKey, text: String, clientMessageId: String, clearDraft: String?) = Unit
        }
        val style = MutableStateFlow(AppStyle.Brief)
        rule.setContent {
            val current = style.collectAsState().value
            PersonaTheme(darkTheme = false, reduceMotion = true, style = current) {
                ChatScreen(key, chats, sender, Center(listOf(answered("new"))), sessions, onBack = {})
            }
        }
        rule.waitUntil(10_000) { rule.onAllNodes(hasText("Rewriting the queries")).fetchSemanticsNodes().isNotEmpty() }
        rule.onNodeWithText("Answered by the assistant").assertDoesNotExist()
        style.value = AppStyle.Glass
        rule.waitUntil(10_000) { rule.onAllNodes(hasText("Answered by the assistant")).fetchSemanticsNodes().isNotEmpty() }
        rule.onNodeWithText("Why: Because new").assertExists()
    }
}
