package persona.feature.chat

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.Modifier
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasContentDescription
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextInput
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.serialization.json.JsonObject
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.*
import persona.core.contracts.api.*
import persona.core.designsystem.PersonaTheme
import persona.core.domain.sort.SessionRenamer
import persona.core.domain.tasks.TasksStore

/**
 * The stack above the composer in the real chat page (#4210 T6, #4339 C10): the composer stays usable while a card waits
 * and a message typed then is sent as any other (as iOS's `sendText`); past half the page they scroll under "N pending";
 * each card folds on its own; Persona's conversation shows its tasks' oldest question under the task's name, and a card
 * the Needs you tray's Review opens joins the stack until it is folded.
 */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en-w400dp-h800dp", application = QuestionTestApplication::class)
class PendingStackUiTest {
    @get:Rule val rule = createComposeRule()
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    @After fun close() { scope.cancel() }

    private val key = SessionKey("computer-a", "importer")
    private val renamer = object : SessionRenamer {
        override fun project(source: Flow<List<Session>>) = source
        override suspend fun rename(session: Session, title: String?, tabName: String?) = false
    }
    private val permission = Interaction("perm", machineId = key.machineId, sessionId = key.sessionId, kind = "permission", toolName = "Bash",
        summary = "npm test", createdAt = "2026-11-12T01:03:00Z")
    private val question = Interaction("ask", machineId = key.machineId, sessionId = key.sessionId, kind = "question", createdAt = "2026-11-12T01:04:00Z",
        questions = listOf(Question("Which branch should I push to?", options = listOf(QuestionOption("main"), QuestionOption("release/1.4")))))
    private val plan = Interaction("plan", machineId = key.machineId, sessionId = key.sessionId, kind = "plan", createdAt = "2026-11-12T01:05:00Z",
        summary = "Retry the import once", plan = "1. Catch the restart error\n2. Retry once")
    private val sent = mutableListOf<String>()

    private fun settle() { Snapshot.sendApplyNotifications(); rule.waitForIdle() }

    private fun agentChat(requests: List<Interaction>) {
        val sessions = object : SessionsRepository {
            override val machines = MutableStateFlow(listOf(Machine(key.machineId, "Computer A", online = true)))
            override val sessions = MutableStateFlow(listOf(Session(key.machineId, key.sessionId, title = "Fix the importer retry",
                agent = "claude", status = "blocked", state = "needs_you", pendingInteractions = requests.size)))
            override suspend fun refresh() = Unit
        }
        val chats = object : ChatRepository {
            override fun observe(session: SessionKey) = MutableStateFlow(ChatSnapshot(loading = false, items = listOf(
                Item("u1", 1, role = "user", kind = "text", text = "Make the importer retry once", ts = "2026-11-12T01:01:00Z"))))
            override suspend fun loadOlder(session: SessionKey) = Unit
            override suspend fun expandFold(session: SessionKey, fromSeq: Int, toSeq: Int): List<Item> = emptyList()
        }
        val sender = object : MessageSender {
            override val outbox = MutableStateFlow(emptyList<OutboxMessage>())
            override suspend fun send(session: SessionKey, text: String, clientMessageId: String, clearDraft: String?) { sent += text }
        }
        val interactions = object : InteractionCenter {
            override val pending = MutableStateFlow(requests)
            override suspend fun refresh() = Unit
            override suspend fun resolve(id: String, resolution: Resolution) = Unit
        }
        rule.setContent {
            PersonaTheme(reduceMotion = true) {
                Box(Modifier.fillMaxSize()) {
                    ChatScreen(key, chats, sender, interactions, sessions, onBack = {}, chrome = ChatChromeBindings(renamer = renamer))
                }
            }
        }
        rule.waitUntil(10_000) { rule.onAllNodesWithText("Make the importer retry once").fetchSemanticsNodes().isNotEmpty() }
        settle()
    }

    @Test fun theComposerStaysAndAMessageTypedWhileAPermissionWaitsIsSent() {
        agentChat(listOf(permission))
        rule.onNodeWithTag("pending-card-perm").assertExists()
        val composer = hasSetTextAction() and !hasAnyAncestor(hasTestTag("pending-card-perm"))
        rule.onNode(composer).performTextInput("Also bump the version")
        settle()
        rule.onNode(hasContentDescription("Send") and !hasAnyAncestor(hasTestTag("pending-card-perm"))).performClick()
        settle()
        assertEquals(listOf("Also bump the version"), sent)
        // the card is still there, unanswered
        rule.onNodeWithTag("pending-card-perm").assertExists()
    }

    /** Past half the page they scroll under "3 pending": the session's requests, the number its Agents row says
     *  ("3 need your decision", one count per card of the session). */
    @Test fun pastHalfThePageTheyScrollUnderNPending() {
        agentChat(listOf(question, plan, permission))
        rule.onNodeWithText("3 pending").assertExists()
        for (id in listOf("perm", "ask", "plan")) rule.onNodeWithTag("pending-card-$id").assertExists()
    }

    @Test fun eachCardFoldsOnItsOwn() {
        agentChat(listOf(permission, question))
        val inQuestion = hasAnyAncestor(hasTestTag("pending-card-ask"))
        rule.onNode(hasContentDescription("Collapse") and hasAnyAncestor(hasTestTag("pending-card-perm"))).performClick()
        settle()
        rule.onNode(hasContentDescription("Expand") and hasAnyAncestor(hasTestTag("pending-card-perm"))).assertExists()
        // the other card is as it was, its options still there
        rule.onNode(hasText("1. main") and inQuestion).assertExists()
        rule.onNode(hasContentDescription("Collapse") and inQuestion).assertExists()
    }

    @Test fun personasStackShowsItsTasksOldestQuestionAndTheTrayReviewBringsInAnother() {
        val main = SessionKey("computer-a", "main")
        val docs = Session("computer-a", "docs", title = "Update the docs", state = "needs_you", status = "blocked", pendingInteractions = 1)
        val release = Session("computer-b", "release", title = "Prepare the release", state = "needs_you", status = "blocked", pendingInteractions = 1)
        val todos = listOf(
            Todo("task-docs", docs.title, origin = "main", agents = listOf(TodoAgent(docs.machineId, docs.sessionId))),
            Todo("task-release", release.title, origin = "main", agents = listOf(TodoAgent(release.machineId, release.sessionId))),
        )
        val docsAsk = Interaction("docs-ask", machineId = docs.machineId, sessionId = docs.sessionId, kind = "question", createdAt = "2026-11-12T01:04:00Z",
            questions = listOf(Question("Should the guide cover Windows too?", options = listOf(QuestionOption("Yes"), QuestionOption("Not yet")))))
        val releaseAsk = Interaction("release-ask", machineId = release.machineId, sessionId = release.sessionId, kind = "question", createdAt = "2026-11-12T01:05:00Z",
            questions = listOf(Question("Which version number?", options = listOf(QuestionOption("1.4.0"), QuestionOption("2.0.0")))))
        val sessions = object : SessionsRepository {
            override val sessions = MutableStateFlow(listOf(Session(main.machineId, main.sessionId, title = "Persona", assistant = true,
                status = "idle", state = "idle"), docs, release))
            override val machines = MutableStateFlow(listOf(Machine("computer-a", "Computer A", online = true), Machine("computer-b", "Computer B", online = true)))
            override suspend fun refresh() = Unit
        }
        val interactions = object : InteractionCenter {
            override val pending = MutableStateFlow(listOf(docsAsk, releaseAsk))
            override suspend fun refresh() = Unit
            override suspend fun resolve(id: String, resolution: Resolution) = Unit
        }
        val connection = object : HubConnection {
            override val state = MutableStateFlow(ConnectionState.Online)
            override val events = MutableSharedFlow<ServerEvent>()
            override fun reconnect() = Unit
            override fun send(message: JsonObject) = true
        }
        val api = object : TasksApi {
            override suspend fun todos() = todos
            override suspend fun patchTodo(id: String, title: String?, notes: String?, done: Boolean?): Todo = error("not used")
            override suspend fun acceptTask(todoId: String, askedAt: String) = Unit
            override suspend fun feed(conversation: String, since: String?) = FeedResponse(emptyList())
            override suspend fun startPlan(id: String, remember: Boolean): TaskPlan = error("not used")
            override suspend fun dismissPlan(id: String): TaskPlan = error("not used")
            override suspend fun retryTask(todoId: String): Todo = error("not used")
            override suspend fun removeTask(todoId: String) = Unit
            override suspend fun route(conversation: String, todoId: String, text: String): FeedItem = error("not used")
            override suspend fun today(conversation: String): FeedItem = error("not used")
            override suspend fun mainConversation() = "main"
            override suspend fun inboxReply(itemId: String, text: String, card: String?) = error("not used")
        }
        val tasks = TasksStore(api, connection, scope).also { it.reloadTodos() }
        val chats = object : ChatRepository {
            override fun observe(session: SessionKey) = MutableStateFlow(ChatSnapshot(loading = false, items = listOf(
                Item("user", 1, role = "user", kind = "text", text = "Keep the docs and the release moving.", ts = "2026-11-12T01:01:00Z"))))
            override suspend fun loadOlder(session: SessionKey) = Unit
            override suspend fun expandFold(session: SessionKey, fromSeq: Int, toSeq: Int) = emptyList<Item>()
        }
        val sender = object : MessageSender {
            override val outbox = MutableStateFlow(emptyList<OutboxMessage>())
            override suspend fun send(session: SessionKey, text: String, clientMessageId: String, clearDraft: String?) = Unit
        }
        rule.setContent {
            PersonaTheme(reduceMotion = true) {
                Box(Modifier.fillMaxSize()) {
                    ChatScreen(main, chats, sender, interactions, sessions, {}, chrome = ChatChromeBindings(tasks = tasks, renamer = renamer),
                        renderStyle = ChatRenderStyle.Assistant, showTopBar = false)
                }
            }
        }
        rule.waitUntil(10_000) { rule.onAllNodesWithText("Keep the docs and the release moving.").fetchSemanticsNodes().isNotEmpty() }
        rule.waitUntil(10_000) { rule.onAllNodes(hasTestTag("pending-card-docs-ask")).fetchSemanticsNodes().isNotEmpty() }
        settle()
        // the oldest question, under its task's name, with how many more; the other is behind the tray
        val inDocs = hasAnyAncestor(hasTestTag("pending-card-docs-ask"))
        rule.onNode(hasText("Update the docs") and inDocs).assertExists()
        rule.onNode(hasText("1 more") and inDocs).assertExists()
        rule.onNodeWithTag("pending-card-release-ask").assertDoesNotExist()
        // the tray's Review for the other one brings it into the stack, once, with its task's name
        rule.onNodeWithTag("assistant-entry-needs").performClick()
        settle()
        rule.onNode(hasText("Review") and hasAnyAncestor(hasTestTag("assistant-needs-task-task-release"))).performClick()
        settle()
        rule.onNodeWithTag("assistant-tray-needs").assertDoesNotExist()
        val inRelease = hasAnyAncestor(hasTestTag("pending-card-release-ask"))
        rule.onNode(hasText("Prepare the release") and inRelease).assertExists()
        rule.onAllNodes(hasTestTag("pending-card-release-ask")).assertCountEquals(1)
        // folded, it goes back to the tray; the oldest stays
        rule.onNode(hasContentDescription("Collapse") and inRelease).performClick()
        settle()
        rule.onNodeWithTag("pending-card-release-ask").assertDoesNotExist()
        rule.onNodeWithTag("pending-card-docs-ask").assertExists()
    }
}
