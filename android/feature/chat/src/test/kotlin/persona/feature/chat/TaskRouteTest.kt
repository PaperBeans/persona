package persona.feature.chat

import androidx.compose.runtime.remember
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.text.TextRange
import androidx.compose.ui.text.input.TextFieldValue
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.serialization.json.JsonObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.FeedItem
import persona.core.contracts.FeedResponse
import persona.core.contracts.ServerEvent
import persona.core.contracts.Session
import persona.core.contracts.SessionKey
import persona.core.contracts.TaskPlan
import persona.core.contracts.Todo
import persona.core.contracts.TodoAgent
import persona.core.contracts.api.ConnectionState
import persona.core.contracts.api.HubConnection
import persona.core.contracts.api.TasksApi
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.Status
import persona.core.domain.tasks.TaskState
import persona.core.domain.tasks.TasksStore

/** Where a message goes in Persona's conversations (#4339 C15, iOS `RouteChip` and `TaskMentionList`). */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en")
class TaskRouteTest {
    @get:Rule val rule = createComposeRule()

    private val login = Todo("login", "Fix the login page", origin = "main", createdAt = "2026-11-12T09:00:00Z",
        agents = listOf(TodoAgent("box", "agent-login", cwd = "/w/app")))
    private val photos = Todo("photos", "Sync HEIC photos", origin = "main", createdAt = "2026-11-12T10:00:00Z")
    private val shipped = Todo("shipped", "Ship the release", origin = "main", createdAt = "2026-11-12T11:00:00Z", doneAt = "2026-11-12T12:00:00Z")
    private val refused = Todo("refused", "Rename the repository", origin = "main", createdAt = "2026-11-12T11:30:00Z",
        doneAt = "2026-11-12T11:31:00Z", startError = "no computer")
    private val elsewhere = Todo("elsewhere", "Plan the trip", origin = "side", createdAt = "2026-11-12T12:00:00Z")

    private class Api(var list: List<Todo>) : TasksApi {
        override suspend fun todos() = list
        override suspend fun feed(conversation: String, since: String?) = FeedResponse(emptyList())
        override suspend fun startPlan(id: String, remember: Boolean): TaskPlan = error("unused")
        override suspend fun dismissPlan(id: String): TaskPlan = error("unused")
        override suspend fun retryTask(todoId: String): Todo = error("unused")
        override suspend fun removeTask(todoId: String) = Unit
        override suspend fun route(conversation: String, todoId: String, text: String): FeedItem = error("unused")
        override suspend fun today(conversation: String): FeedItem = error("unused")
        override suspend fun mainConversation(): String? = "main"
        override suspend fun inboxReply(itemId: String, text: String, card: String?) = Unit
    }

    private class Connection : HubConnection {
        override val state = MutableStateFlow(ConnectionState.WaitingForNetwork)
        override val events = MutableSharedFlow<ServerEvent>()
        override fun reconnect() = Unit
        override fun send(message: JsonObject): Boolean = true
    }

    private fun store(vararg todos: Todo) =
        TasksStore(Api(todos.toList()), Connection(), CoroutineScope(Dispatchers.Unconfined)).also { it.reloadTodos() }

    @Test fun theAtListOffersTheConversationsOpenTasksNewestFirst() {
        val all = listOf(login, photos, shipped, refused, elsewhere)
        val offered = mentionTasks(all, "main") { TaskState.of(it, emptyList()) }
        // Done and failed tasks have nobody to say a word to; another conversation's tasks are not this one's.
        assertEquals(listOf("photos", "login"), offered.map { it.first.id })
        assertEquals(listOf(TaskState.NotStarted, TaskState.NotStarted), offered.map { it.second })
        assertEquals(emptyList<String>(), mentionTasks(all, "nobody") { TaskState.of(it, emptyList()) }.map { it.first.id })
    }

    @Test fun tasksComeFirstAndTheTypedWordNarrowsThem() {
        val tasks = listOf(MentionTask("photos", "Sync HEIC photos", "", Status.Working), MentionTask("login", "Fix the login page", "Box · app", Status.NeedsYou))
        val other = Session(machineId = "box", sessionId = "s2", title = "Photo sync", cwd = "/w/photos", updatedAt = "2026-11-05T09:00:00Z")
        val mentions = ComposerMentions(listOf("/w/app/Login.kt"), "/w/app", listOf(other), SessionKey("box", "main"), tasks)
        assertEquals(listOf("task:photos", "task:login", "file:/w/app/Login.kt", "session:box/s2"), mentionRows(mentions, "").map { it.key })
        assertEquals(listOf("task:login", "file:/w/app/Login.kt"), mentionRows(mentions, "LOGIN").map { it.key })
        assertEquals(listOf("task:photos", "session:box/s2"), mentionRows(mentions, "photo").map { it.key })
        // An Agent's chat has no tasks in its list.
        assertEquals(listOf("file:/w/app/Login.kt"), mentionRows(mentions.copy(tasks = null), "log").map { it.key })
    }

    @Test fun theChipNamesTheRoutedTaskOnlyWhileItsTodoIsListed() {
        assertNull(routedTask(emptyMap(), listOf(login), "main"))
        assertNull(routedTask(mapOf("main" to "gone"), listOf(login), "main"))
        assertNull(routedTask(mapOf("side" to "login"), listOf(login), "main"))
        assertEquals(login, routedTask(mapOf("main" to "login"), listOf(login, photos), "main"))
    }

    @Test fun pickingATaskAfterTheAtRoutesTheNextMessageAndTakesTheAtOut() {
        val picked = mutableListOf<String>()
        val state = ChatComposerState().apply { value = TextFieldValue("Please @", TextRange(8)) }
        val tasks = listOf(MentionTask("login", "Fix the login page", "Box · app", Status.NeedsYou))
        val other = Session(machineId = "box", sessionId = "s2", title = "Photo sync", cwd = "/w/photos")
        rule.setContent {
            PersonaTheme(darkTheme = false, reduceMotion = true, style = AppStyle.Glass) {
                ChatComposer(SessionKey("box", "main"), remember { state }, working = false, sending = false, canClear = false, service = null,
                    onSend = {}, onCommand = {}, onNotice = {},
                    mentions = ComposerMentions(emptyList(), "", listOf(other), SessionKey("box", "main"), tasks), onMentionTask = { picked += it })
            }
        }
        rule.onNodeWithText("Message a task").assertIsDisplayed()
        rule.onNodeWithText("Mention a file or session").assertIsDisplayed()
        rule.onNodeWithText("Fix the login page").performClick()
        rule.waitForIdle()
        assertEquals(listOf("login"), picked)
        assertEquals("Please ", state.value.text)
    }

    @Test fun theChipSaysWhereTheNextMessageGoesAndItsXHandsItBackToPersona() {
        val tasks = store(login, photos)
        val host = AssistantTaskHost(tasks, "main", emptyList(), { "Box" }, {}, { throw it }, {})
        rule.setContent {
            PersonaTheme(darkTheme = false, reduceMotion = true, style = AppStyle.Glass) { TaskRouteChip(host) }
        }
        rule.onNodeWithText("Message “Fix the login page”").assertDoesNotExist()
        tasks.setRoute("main", "login")
        rule.onNodeWithText("Message “Fix the login page”").assertIsDisplayed()
        rule.onNodeWithContentDescription("Hand to Persona").performClick()
        rule.waitForIdle()
        assertEquals(emptyMap<String, String>(), tasks.routes.value)
        rule.onNodeWithText("Message “Fix the login page”").assertDoesNotExist()
    }
}
