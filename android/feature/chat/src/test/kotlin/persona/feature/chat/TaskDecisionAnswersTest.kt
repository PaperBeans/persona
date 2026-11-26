package persona.feature.chat

import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertHeightIsAtLeast
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsEnabled
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.serialization.json.JsonObject
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.AskOption
import persona.core.contracts.FeedItem
import persona.core.contracts.FeedResponse
import persona.core.contracts.ServerEvent
import persona.core.contracts.Session
import persona.core.contracts.SessionKey
import persona.core.contracts.Supervision
import persona.core.contracts.TaskPlan
import persona.core.contracts.Todo
import persona.core.contracts.TodoAsk
import persona.core.contracts.api.AskAnswer
import persona.core.contracts.api.ConnectionState
import persona.core.contracts.api.HubConnection
import persona.core.contracts.api.TasksApi
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.Tokens
import persona.core.domain.tasks.TasksStore
import java.time.Instant

@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en-w360dp-h800dp-xhdpi")
class TaskDecisionAnswersTest {
    @get:Rule val rule = createComposeRule()
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)

    @After fun close() {
        scope.cancel()
    }

    private val askedAt = Instant.now().minusSeconds(600).toString()

    private val connection = object : HubConnection {
        override val state = MutableStateFlow<ConnectionState>(ConnectionState.WaitingForNetwork)
        override val events = MutableSharedFlow<ServerEvent>()
        override fun reconnect() = Unit
        override fun send(message: JsonObject) = true
    }

    private class FakeTasksApi(
        private val rows: List<Todo>,
        private val onAnswer: suspend (String, String, AskAnswer) -> Unit = { _, _, _ -> },
    ) : TasksApi {
        val answers = mutableListOf<Pair<String, AskAnswer>>()

        override suspend fun taskAgent(key: SessionKey): Session? = null
        override suspend fun todos(): List<Todo> = rows
        override suspend fun patchTodo(id: String, title: String?, notes: String?, done: Boolean?): Todo = error("unused")
        override suspend fun answerTask(todoId: String, askedAt: String, answer: AskAnswer) {
            onAnswer(todoId, askedAt, answer)
            answers += todoId to answer
        }
        override suspend fun feed(conversation: String, since: String?) = FeedResponse(emptyList())
        override suspend fun startPlan(id: String, remember: Boolean): TaskPlan = error("unused")
        override suspend fun dismissPlan(id: String): TaskPlan = error("unused")
        override suspend fun retryTask(todoId: String): Todo = error("unused")
        override suspend fun removeTask(todoId: String) = Unit
        override suspend fun route(conversation: String, todoId: String, text: String): FeedItem = error("unused")
        override suspend fun today(conversation: String): FeedItem = error("unused")
        override suspend fun mainConversation() = "main"
        override suspend fun inboxReply(itemId: String, text: String, card: String?) = Unit
    }

    private fun todoWithAsk(
        whyOwner: String = "priority",
        options: List<AskOption> = listOf(
            AskOption("Accept and close task directly", "Closes the task and records the verified result"),
            AskOption("Keep task open for follow-up", "Leaves the task open so another round can continue"),
            AskOption("Cancel release"),
        ),
    ): Pair<Todo, TodoAsk> {
        val ask = TodoAsk(
            outcome = "decide",
            verdict = "Needs your decision",
            recommend = options.first().label,
            askedAt = askedAt,
            question = "How should we finish this task?",
            whyOwner = whyOwner,
            options = options,
            responses = listOf("decide_yourself", "explain_first"),
        )
        val todo = Todo(
            id = "todo-decision-1",
            title = "Release verification",
            origin = "main",
            createdAt = askedAt,
            updatedAt = askedAt,
            supervision = Supervision(state = "needs_user"),
            ask = ask,
        )
        return todo to ask
    }

    private fun hostFor(api: TasksApi): AssistantTaskHost {
        val tasks = TasksStore(api, connection, scope).also { it.reloadTodos() }
        return AssistantTaskHost(
            tasks = tasks,
            conversation = "main",
            sessions = emptyList(),
            machineName = { it },
            onOpen = {},
            onError = {},
            say = {},
        )
    }

    @Test fun clickingFirstOptionRowSubmitsFirstOptionAndDisablesRowsAfterward() {
        val (todo, ask) = todoWithAsk()
        val api = FakeTasksApi(listOf(todo))
        val host = hostFor(api)

        rule.setContent {
            PersonaTheme {
                TaskDecisionAnswers(todo = todo, ask = ask, host = host)
            }
        }

        rule.onNodeWithTag("decision-leads-0")
            .assertIsDisplayed()
            .assertIsEnabled()
            .assert(SemanticsMatcher.expectValue(SemanticsProperties.Role, Role.Button))
            .performClick()
        rule.waitForIdle()

        assertEquals(listOf(todo.id to AskAnswer.Option("Accept and close task directly")), api.answers)
        rule.onNodeWithTag("decision-done", useUnmergedTree = true).assertTextEquals("Answered: Accept and close task directly")
        rule.onNodeWithTag("decision-leads-0").assertIsNotEnabled()
        rule.onNodeWithTag("decision-leads-1").assertIsNotEnabled()
    }

    @Test fun clickingSecondOptionRowSubmitsSecondOptionWhilePreservingBottomActionBar() {
        val (todo, ask) = todoWithAsk()
        val api = FakeTasksApi(listOf(todo))
        val host = hostFor(api)

        rule.setContent {
            PersonaTheme {
                TaskDecisionAnswers(todo = todo, ask = ask, host = host)
            }
        }

        // Bottom quick-action bar remains intact (recommended primary button, Other…, and More answers menu).
        rule.onNode(hasText("Accept and close task directly") and hasClickAction() and hasAnyAncestor(hasTestTag("decision-answers")))
            .assertIsDisplayed()
        rule.onNodeWithText("Other…", useUnmergedTree = true).assertIsDisplayed()
        rule.onNodeWithContentDescription("More answers").assertIsDisplayed()

        // Tapping the second option row directly answers with the second option without opening the overflow menu.
        rule.onNodeWithTag("decision-leads-1")
            .assertIsDisplayed()
            .assertIsEnabled()
            .assert(SemanticsMatcher.expectValue(SemanticsProperties.Role, Role.Button))
            .performClick()
        rule.waitForIdle()

        assertEquals(listOf(todo.id to AskAnswer.Option("Keep task open for follow-up")), api.answers)
        rule.onNodeWithTag("decision-done", useUnmergedTree = true).assertTextEquals("Answered: Keep task open for follow-up")
    }

    @Test fun confirmsOptionsArmsOptionRowBeforeSubmittingAndSwitchesCleanly() {
        val (todo, ask) = todoWithAsk(whyOwner = "irreversible")
        val api = FakeTasksApi(listOf(todo))
        val host = hostFor(api)

        rule.setContent {
            PersonaTheme {
                TaskDecisionAnswers(todo = todo, ask = ask, host = host)
            }
        }

        // First tap on the second option arms it without submitting.
        rule.onNodeWithTag("decision-leads-1").performClick()
        rule.waitForIdle()

        assertTrue(api.answers.isEmpty())
        rule.onNodeWithTag("decision-leads-confirm-1", useUnmergedTree = true)
            .assertIsDisplayed()
            .assertTextEquals("Tap again to confirm")
        rule.onNodeWithTag("decision-leads-1")
            .assert(SemanticsMatcher.expectValue(SemanticsProperties.StateDescription, "Tap again to confirm"))
        rule.onAllNodesWithTag("decision-leads-confirm-0", useUnmergedTree = true).assertCountEquals(0)

        // Tapping another option switches armed state to that option without submitting.
        rule.onNodeWithTag("decision-leads-0").performClick()
        rule.waitForIdle()

        assertTrue(api.answers.isEmpty())
        rule.onAllNodesWithTag("decision-leads-confirm-1", useUnmergedTree = true).assertCountEquals(0)
        rule.onNodeWithTag("decision-leads-confirm-0", useUnmergedTree = true)
            .assertIsDisplayed()
            .assertTextEquals("Tap again to confirm")
        rule.onNode(hasText("Confirm: Accept and close task directly") and hasClickAction()).assertIsDisplayed()

        // Second tap on the armed row submits the answer and clears the armed prompt.
        rule.onNodeWithTag("decision-leads-0").performClick()
        rule.waitForIdle()

        assertEquals(listOf(todo.id to AskAnswer.Option("Accept and close task directly")), api.answers)
        rule.onAllNodesWithTag("decision-leads-confirm-0", useUnmergedTree = true).assertCountEquals(0)
        rule.onNodeWithTag("decision-done", useUnmergedTree = true).assertTextEquals("Answered: Accept and close task directly")
    }

    @Test fun disabledStateBlocksClicksOnOptionRows() {
        val (todo, ask) = todoWithAsk()
        val api = FakeTasksApi(listOf(todo))
        val host = hostFor(api)

        rule.setContent {
            PersonaTheme {
                TaskDecisionAnswers(todo = todo, ask = ask, host = host, enabled = false)
            }
        }

        rule.onNodeWithTag("decision-leads-0").assertIsNotEnabled()
        rule.onNodeWithTag("decision-leads-1").assertIsNotEnabled()
        rule.onNodeWithTag("decision-leads-0").performClick()
        rule.onNodeWithTag("decision-leads-1").performClick()
        rule.waitForIdle()

        assertTrue(api.answers.isEmpty())
    }

    @Test fun sendingStateDisablesOptionRowsWhileRequestIsInFlight() {
        val gate = CompletableDeferred<Unit>()
        val (todo, ask) = todoWithAsk()
        val api = FakeTasksApi(listOf(todo), onAnswer = { _, _, _ -> gate.await() })
        val host = hostFor(api)

        rule.setContent {
            PersonaTheme {
                TaskDecisionAnswers(todo = todo, ask = ask, host = host)
            }
        }

        rule.onNodeWithTag("decision-leads-0").performClick()
        rule.waitForIdle()

        // While sending is in flight, option rows are disabled and ignore further taps.
        rule.onNodeWithTag("decision-leads-0").assertIsNotEnabled()
        rule.onNodeWithTag("decision-leads-1").assertIsNotEnabled()
        rule.onNodeWithTag("decision-leads-1").performClick()
        rule.waitForIdle()

        gate.complete(Unit)
        rule.waitForIdle()

        assertEquals(listOf(todo.id to AskAnswer.Option("Accept and close task directly")), api.answers)
    }

    @Test fun optionRowsMeetMinimumTouchHeightEvenWithoutLeadsTo() {
        val (todo, ask) = todoWithAsk()
        val api = FakeTasksApi(listOf(todo))
        val host = hostFor(api)

        rule.setContent {
            PersonaTheme {
                TaskDecisionAnswers(todo = todo, ask = ask, host = host)
            }
        }

        rule.onNodeWithTag("decision-leads-0").assertHeightIsAtLeast(Tokens.MinTouch.android)
        rule.onNodeWithTag("decision-leads-1").assertHeightIsAtLeast(Tokens.MinTouch.android)
        rule.onNodeWithTag("decision-leads-2").assertHeightIsAtLeast(Tokens.MinTouch.android)
    }
}
