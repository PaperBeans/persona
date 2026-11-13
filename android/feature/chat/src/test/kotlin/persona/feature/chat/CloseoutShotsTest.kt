package persona.feature.chat

import android.content.res.Configuration
import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.runtime.*
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.*
import kotlinx.serialization.json.JsonObject
import org.junit.After
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.*
import persona.core.contracts.WorkState
import persona.core.contracts.api.*
import persona.core.designsystem.*
import persona.core.domain.tasks.TasksStore
import java.io.File
import java.time.Instant

/**
 * The closeout card above a stopped agent's transcript (#3612), the decision answered on its Needs you row (#3804) and
 * "nobody is working on this" on a task's row (#3618), drawn by the real ChatScreen in every style, light and dark.
 * PNGs in `build/closeout-shots/`. Native JVM rendering: no system bars, no glass refraction, no device.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h900dp-xhdpi")
class CloseoutShotsTest {
    @get:Rule val rule = createComposeRule()
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    @After fun close() { scope.cancel() }

    private val block = "<closeout status=\"done\" merged=\"yes\" deployed=\"no\">\ndid: Retry the import once when the hub restarts\n" +
        "artifact: pr https://example.com/acme/app/pull/128\nverified: unit tests pass\n</closeout>"
    private val agentKey = SessionKey("computer-a", "importer")
    private fun agent(work: WorkState?, state: String = "unread", status: String = "done") = Session(agentKey.machineId, agentKey.sessionId,
        title = "Fix the importer retry", cwd = "/home/me/code/app", state = state, status = status, lastText = "The retry is fixed.", work = work)

    private val artifacts = listOf(WorkArtifact("pr", "https://example.com/acme/app/pull/128"), WorkArtifact("commit", "4f2a9c1"),
        WorkArtifact("doc", "https://example.com/docs/importer-retry"))
    private val confirmed = WorkState("done", line = "Retry the import once when the hub restarts", artifacts = artifacts,
        closeout = Closeout("done", did = listOf("Retry the import once when the hub restarts", "Added a test for the restart path"),
            merged = "yes", deployed = "no", verified = "Unit tests pass; the restart was tried once by hand",
            checked = CloseoutChecked(merged = "yes")))
    private val contradicted = WorkState("", line = "Says merged, but the pull request is not merged", artifacts = artifacts.take(1),
        closeout = Closeout("done", did = listOf("Retry the import once when the hub restarts"), merged = "yes", deployed = "yes",
            verified = "untested", missing = "A review of the pull request", checked = CloseoutChecked(merged = "no", deployed = "no")))
    private val blocked = WorkState("blocked", on = "external", line = "Waits for the signing key",
        closeout = Closeout("blocked", waiting = "the signing key from the release owner", did = listOf("Prepared the release build"), merged = "no"))

    private val connection = object : HubConnection {
        override val state = MutableStateFlow(ConnectionState.WaitingForNetwork)
        override val events = MutableSharedFlow<ServerEvent>()
        override fun reconnect() = Unit
        override fun send(message: JsonObject) = true
    }
    private val sender = object : MessageSender {
        override val outbox = MutableStateFlow(emptyList<OutboxMessage>())
        override suspend fun send(session: SessionKey, text: String, clientMessageId: String, clearDraft: String?) = Unit
    }
    private val noRequests = object : InteractionCenter {
        override val pending = MutableStateFlow(emptyList<Interaction>())
        override suspend fun refresh() = Unit
        override suspend fun resolve(id: String, resolution: Resolution) = Unit
    }
    private fun chats(vararg items: Item) = object : ChatRepository {
        override fun observe(session: SessionKey) = flowOf(ChatSnapshot(loading = false, items = items.toList()))
        override suspend fun loadOlder(session: SessionKey) = Unit
        override suspend fun expandFold(session: SessionKey, fromSeq: Int, toSeq: Int) = emptyList<Item>()
    }
    private open inner class Tasks(var rows: List<Todo> = emptyList(), val whole: (SessionKey) -> Session? = { null }) : TasksApi {
        val answers = mutableListOf<Pair<String, AskAnswer>>()
        override suspend fun taskAgent(key: SessionKey) = whole(key)
        override suspend fun todos() = rows
        override suspend fun patchTodo(id: String, title: String?, notes: String?, done: Boolean?): Todo = error("not used")
        override suspend fun answerTask(todoId: String, askedAt: String, answer: AskAnswer) { answers += todoId to answer }
        override suspend fun feed(conversation: String, since: String?) = FeedResponse(emptyList())
        override suspend fun startPlan(id: String, remember: Boolean): TaskPlan = error("not used")
        override suspend fun dismissPlan(id: String): TaskPlan = error("not used")
        override suspend fun retryTask(todoId: String): Todo = error("not used")
        override suspend fun removeTask(todoId: String) = Unit
        override suspend fun route(conversation: String, todoId: String, text: String): FeedItem = error("not used")
        override suspend fun today(conversation: String): FeedItem = error("not used")
        override suspend fun mainConversation() = "main"
        override suspend fun inboxReply(itemId: String, text: String, card: String?) = Unit
    }

    private fun settle() {
        Snapshot.sendApplyNotifications()
        repeat(5) { rule.mainClock.advanceTimeByFrame() }
        // long enough for a press's ripple to be gone before a capture
        rule.mainClock.advanceTimeBy(1_200)
        rule.waitForIdle()
    }
    private fun shot(name: String): Int {
        val image = rule.onNodeWithTag("closeout-shot").captureToImage().asAndroidBitmap()
        val output = File(System.getProperty("persona.shots") ?: "build/closeout-shots").also { it.mkdirs() }
        File(output, "$name.png").outputStream().use { assertTrue(image.compress(Bitmap.CompressFormat.PNG, 100, it)) }
        return image.getPixel(2, 2)
    }
    private fun variant(style: AppStyle, dark: Boolean) = "${style.name.lowercase()}-${if (dark) "dark" else "light"}"

    @Composable
    private fun Page(style: AppStyle, dark: Boolean, large: Boolean = false, content: @Composable () -> Unit) {
        val configuration = Configuration(LocalConfiguration.current).apply {
            fontScale = if (large) 1.6f else 1f
            uiMode = (uiMode and Configuration.UI_MODE_NIGHT_MASK.inv()) or
                if (dark) Configuration.UI_MODE_NIGHT_YES else Configuration.UI_MODE_NIGHT_NO
        }
        // large text needs the density's font scale too, not only the configuration's
        val density = androidx.compose.ui.unit.Density(androidx.compose.ui.platform.LocalDensity.current.density, if (large) 1.6f else 1f)
        CompositionLocalProvider(LocalConfiguration provides configuration, androidx.compose.ui.platform.LocalDensity provides density) {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                Box(Modifier.fillMaxSize().background(PersonaTheme.palette.background).testTag("closeout-shot")) { content() }
            }
        }
    }

    @Test fun theCardStandsAboveTheTranscriptAndTheReplyLosesOnlyItsBlock() {
        // the list row is a summary: where the closeout stands; the whole row has what the agent did and produced
        fun summary(work: WorkState) = work.copy(artifacts = null, closeout = work.closeout!!.copy(did = null, verified = null))
        var work by mutableStateOf(confirmed)
        val rows = MutableStateFlow(listOf(agent(summary(confirmed)).copy(waiting = false)))
        val sessions = object : SessionsRepository {
            override val sessions = rows
            override val machines = MutableStateFlow(listOf(Machine("computer-a", "Computer A", online = true)))
            override suspend fun refresh() = Unit
        }
        val tasks = TasksStore(Tasks(whole = { agent(work) }), connection, scope)
        val transcript = chats(
            Item("user", 1, role = "user", kind = "text", text = "The import fails when the hub restarts. Fix it.", ts = "2026-11-10T09:00:00Z"),
            Item("answer", 2, role = "assistant", kind = "text", ts = "2026-11-10T09:20:00Z",
                // fenced, as some agents write it: without the card this draws as a code box
                text = "The import now retries once when the hub restarts, and a test covers the restart path.\n\n```\n$block\n```"),
        )
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        var large by mutableStateOf(false)
        rule.mainClock.autoAdvance = false
        rule.setContent {
            Page(style, dark, large) {
                ChatScreen(agentKey, transcript, sender, noRequests, sessions, {}, chrome = ChatChromeBindings(tasks = tasks))
            }
        }
        settle()
        for (s in AppStyle.entries) {
            val corners = mutableListOf<Int>()
            for (d in listOf(false, true)) {
                style = s; dark = d
                work = confirmed; rows.value = listOf(agent(summary(confirmed)).copy(waiting = false, rev = 1)); settle()
                rule.onNodeWithTag("closeout-card").assertIsDisplayed()
                rule.onNodeWithText("The agent's own report").assertIsDisplayed()
                // what the whole row adds to the summary
                rule.onNodeWithText("Added a test for the restart path").assertIsDisplayed()
                rule.onNodeWithText("PR #128").assertIsDisplayed()
                rule.onNodeWithTag("closeout-claim-merged-confirmed", useUnmergedTree = true).assertIsDisplayed()
                rule.onNodeWithText("Hub confirmed", useUnmergedTree = true).assertIsDisplayed()
                rule.onNodeWithTag("closeout-claim-deployed-unchecked", useUnmergedTree = true).assertIsDisplayed()
                rule.onNodeWithText("Not checked by the hub", useUnmergedTree = true).assertIsDisplayed()
                // the reply keeps its words and loses exactly the block
                rule.onNodeWithText("The import now retries once", substring = true).assertIsDisplayed()
                rule.onAllNodesWithText("<closeout", substring = true).assertCountEquals(0)
                rule.onAllNodesWithText("did: Retry the import", substring = true).assertCountEquals(0)
                corners += shot("card-confirmed-${variant(s, d)}")

                work = contradicted; rows.value = listOf(agent(summary(contradicted)).copy(waiting = false, rev = 2)); settle()
                rule.onNodeWithTag("closeout-claim-merged-contradicted", useUnmergedTree = true).assertIsDisplayed()
                rule.onNodeWithText("Hub found it isn't merged", useUnmergedTree = true).assertIsDisplayed()
                rule.onNodeWithText("Hub found it isn't deployed", useUnmergedTree = true).assertIsDisplayed()
                rule.onNodeWithText("A review of the pull request").assertIsDisplayed()
                shot("card-contradicted-${variant(s, d)}")

                work = blocked; rows.value = listOf(agent(summary(blocked)).copy(waiting = false, rev = 3)); settle()
                rule.onNodeWithText("Blocked on: the signing key from the release owner").assertIsDisplayed()
                rule.onNodeWithTag("closeout-claim-merged-unchecked", useUnmergedTree = true).assertIsDisplayed()
                shot("card-blocked-${variant(s, d)}")
                // folded, the head alone says where it stands
                rule.onNodeWithTag("closeout-head").performClick(); settle()
                rule.onAllNodesWithTag("closeout-fields").assertCountEquals(0)
                shot("card-folded-${variant(s, d)}")
                rule.onNodeWithTag("closeout-head").performClick(); settle()
            }
            assertNotEquals("Light and dark captures must differ", corners[0], corners[1])
        }
        // the largest text: the head stays one line each, the fields wrap and scroll within their share of the window
        style = AppStyle.Glass; dark = false; large = true
        work = contradicted; rows.value = listOf(agent(summary(contradicted)).copy(waiting = false, rev = 5)); settle()
        rule.onNodeWithTag("closeout-claim-merged-contradicted", useUnmergedTree = true).assertExists()
        shot("card-contradicted-glass-light-large")
        large = false; settle()
        // a new turn has no closeout yet: the card goes and the transcript is whole again
        rows.value = listOf(agent(summary(confirmed), state = "working", status = "working").copy(waiting = false, rev = 4)); settle()
        rule.onAllNodesWithTag("closeout-card").assertCountEquals(0)
        // The restored fence parses off the UI thread; pump this fixture's paused clock until it is drawn.
        rule.waitUntil(10_000) {
            rule.mainClock.advanceTimeByFrame()
            rule.onAllNodesWithText("did: Retry the import", substring = true).fetchSemanticsNodes().isNotEmpty()
        }
        shot("card-none-working")
        rule.onNodeWithText("did: Retry the import", substring = true).assertExists()
    }

    private val main = SessionKey("computer-a", "main")
    private val stopped = Session("computer-b", "stopped", title = "Migrate the billing tables", state = "idle", status = "idle", lastText = "API error 400")
    private val release = Session("computer-b", "release", title = "Prepare the 2.4 release", state = "idle", status = "idle", lastText = "Ready to release.")
    // Recent, by the real clock the trays read: a task idle for 48 h is shelved and leaves In progress (TodoTriage, #5530).
    private val askedAt = Instant.now().minusSeconds(2 * 3_600).toString()
    private val decision = Todo("task-release", release.title, origin = "main", createdAt = askedAt, updatedAt = askedAt,
        agents = listOf(TodoAgent(release.machineId, release.sessionId)), supervision = Supervision(state = "needs_user"),
        ask = TodoAsk("decide", "Needs your decision: release 2.4 to everyone now?", "Release now", askedAt,
            question = "Release 2.4 to everyone now, or wait for the crash fix?", whyOwner = "irreversible",
            background = "From your message on Nov 8 (\"get 2.4 out this week\"). The build passed its tests and is not deployed.",
            options = listOf(AskOption("Release now", "Everyone gets 2.4 today; the crash fix follows in 2.4.1"),
                AskOption("Wait for the crash fix", "2.4 goes out with the fix, about two days later"),
                AskOption("Release to 10% first", "A staged rollout; widen it once the crash rate is known")),
            costOfWaiting = "The importer fix in 2.4 stays unreleased", default = "never",
            responses = listOf("decide_yourself", "not_what_i_asked", "explain_first")))
    private val orphan = Todo("task-billing", stopped.title, origin = "main", createdAt = askedAt, updatedAt = askedAt,
        agents = listOf(TodoAgent(stopped.machineId, stopped.sessionId)), orphaned = TodoOrphaned("error", askedAt))

    @Test
    @Config(sdk = [35], qualifiers = "en-w400dp-h1000dp-xhdpi")
    fun aDecisionIsAnsweredOnItsRowAndAnOrphanedTaskSaysSo() {
        val api = Tasks(listOf(decision, orphan))
        val tasks = TasksStore(api, connection, scope).also { it.reloadTodos() }
        val sessions = object : SessionsRepository {
            override val sessions = MutableStateFlow(listOf(Session(main.machineId, main.sessionId, title = "Persona", assistant = true, status = "done", state = "idle"), stopped, release))
            override val machines = MutableStateFlow(listOf(Machine("computer-a", "Computer A", online = true), Machine("computer-b", "Computer B", online = true)))
            override suspend fun refresh() = Unit
        }
        val inbox = object : AssistantInboxTrays {
            override fun drafts(scope: String) = MutableStateFlow(emptyList<AssistantInboxDraft>())
            override suspend fun refresh(scope: String) = Unit
            override suspend fun send(scope: String, draft: AssistantInboxDraft, clientId: String) = false
        }
        val transcript = chats(
            Item("user", 1, role = "user", kind = "text", text = "Get 2.4 out this week.", ts = "2026-11-08T09:00:00Z"),
            Item("answer", 2, role = "assistant", kind = "text", text = "Two tasks are running for it. One needs your decision.", ts = "2026-11-08T09:00:01Z"),
        )
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        var large by mutableStateOf(false)
        rule.mainClock.autoAdvance = false
        rule.setContent {
            Page(style, dark, large) {
                ChatScreen(main, transcript, sender, noRequests, sessions, {},
                    chrome = ChatChromeBindings(tasks = tasks, inboxTrays = inbox, inboxScope = "fixture"),
                    renderStyle = ChatRenderStyle.Assistant, embedded = true, showTopBar = false)
            }
        }
        settle()
        for (s in AppStyle.entries) for (d in listOf(false, true)) {
            style = s; dark = d; settle()
            rule.onNodeWithTag("assistant-entry-needs").performClick(); settle()
            rule.onNodeWithTag("assistant-task-decision-${decision.id}").assertIsDisplayed()
            rule.onNodeWithTag("decision-question", useUnmergedTree = true).assertTextEquals("Release 2.4 to everyone now, or wait for the crash fix?")
            rule.onNodeWithTag("decision-background", useUnmergedTree = true).assertIsDisplayed()
            rule.onNodeWithText("Everyone gets 2.4 today; the crash fix follows in 2.4.1", useUnmergedTree = true).assertIsDisplayed()
            rule.onNodeWithText("Recommended", useUnmergedTree = true).assertIsDisplayed()
            rule.onNodeWithTag("decision-default", useUnmergedTree = true)
                .assertTextEquals("Never taken by default · If it waits: The importer fix in 2.4 stays unreleased")
            shot("decision-${variant(s, d)}")
            // four lines of options are taller than the tray: the answers are one short scroll away
            rule.onNodeWithTag("assistant-task-decision-${decision.id}").performTouchInput { swipeUp() }; settle()
            shot("decision-answers-${variant(s, d)}")
            rule.onNodeWithText("Other…", useUnmergedTree = true).assertIsDisplayed()
            rule.onNodeWithContentDescription("More answers").assertIsDisplayed()
            // a step that cannot be undone: the first press arms the option and sends nothing
            rule.onNode(hasText("Release now") and hasClickAction()).performClick(); settle()
            assertTrue(api.answers.isEmpty())
            rule.onNode(hasText("Confirm: Release now") and hasClickAction()).assertIsDisplayed()
            shot("decision-armed-${variant(s, d)}")
            // the fixed answers and the options that did not fit are in the menu
            rule.onNodeWithContentDescription("More answers").performClick(); settle()
            rule.onNodeWithText("You decide").assertIsDisplayed()
            rule.onNodeWithText("This isn't what I asked for").assertIsDisplayed()
            rule.onNodeWithText("Tell me the background first").assertIsDisplayed()
            rule.onNodeWithText("Tell me the background first").performClick(); settle()
            assertEquals(decision.id to AskAnswer.Response("explain_first") as AskAnswer, api.answers.removeLast())
            rule.onNodeWithTag("decision-done", useUnmergedTree = true).assertTextEquals("Sent back to Persona")
            // the next variant starts from the unanswered round again
            tasks.reloadTodos(); settle()
            rule.onNodeWithTag("assistant-entry-running").performClick(); settle()
            rule.onNodeWithTag("assistant-orphaned-todo:${orphan.id}", useUnmergedTree = true)
                .assertTextEquals("Nobody is working on this · closed after an error")
            shot("orphaned-${variant(s, d)}")
            rule.onNodeWithTag("assistant-entry-running").performClick(); settle()
        }
        // the largest text: the buttons keep one line each; what no longer fits is in the menu
        style = AppStyle.Glass; dark = false; large = true; settle()
        rule.onNodeWithTag("assistant-entry-needs").performClick(); settle()
        rule.onNodeWithTag("assistant-task-decision-${decision.id}").performTouchInput { swipeUp() }; settle()
        rule.onNodeWithText("Other…", useUnmergedTree = true).assertExists()
        shot("decision-answers-glass-light-large")
    }
}
