package persona.feature.agents

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.SemanticsNodeInteraction
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTouchInput
import androidx.compose.ui.test.swipeLeft
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.serialization.json.JsonObject
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.FeedItem
import persona.core.contracts.FeedResponse
import persona.core.contracts.Hello
import persona.core.contracts.ServerEvent
import persona.core.contracts.TaskPlan
import persona.core.contracts.Todo
import persona.core.contracts.TodoDue
import persona.core.contracts.api.ConnectionState
import persona.core.contracts.api.HubConnection
import persona.core.contracts.api.TasksApi
import persona.core.designsystem.AppFeedback
import persona.core.designsystem.CalendarTokens
import persona.core.designsystem.FeedbackAction
import persona.core.designsystem.LocalAppFeedback
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.Tokens
import persona.core.domain.tasks.TasksStore
import java.time.Instant
import java.time.LocalDate
import java.util.TimeZone

/**
 * Moving a to-do on the month (#2045): a long press picks it up and a drop on another day saves it there at the same
 * time, with "Moved to …" and Undo; dropped where it was, nothing is sent; a failed save puts it back and says why; a
 * swipe still turns the month; TalkBack's "Change Due Date…" moves one without dragging.
 */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "zh-w400dp-h860dp-xhdpi")
class TodoCalendarDragTest {
    @get:Rule val rule = createComposeRule()

    private val zone = TimeZone.getDefault()
    @Before fun inShanghai() = TimeZone.setDefault(TimeZone.getTimeZone("Asia/Shanghai"))
    @After fun back() = TimeZone.setDefault(zone)

    // 17:30 on Thursday, November 5 2026, in the test's zone
    private val now = Instant.parse("2026-11-05T09:30:00Z")
    // 09:00 on Nov 6; all day on Nov 12
    private val checkup = Todo("t5", title = "体检预约", dueAt = "2026-11-06T01:00:00Z", remindAt = "2026-11-06T00:00:00Z")
    private val review = Todo("t6", title = "评审设计稿", dueDate = "2026-11-12")

    private class Api(var rows: List<Todo>) : TasksApi {
        val sent = mutableListOf<Pair<String, TodoDue>>()
        var failure: Throwable? = null
        override suspend fun todos() = rows
        override suspend fun setTodoDue(id: String, due: TodoDue): Todo {
            sent += id to due
            failure?.let { throw it }
            return rows.first { it.id == id }.withDue(due).also { moved -> rows = rows.map { if (it.id == id) moved else it } }
        }
        override suspend fun feed(conversation: String, since: String?) = FeedResponse(emptyList(), emptyMap())
        override suspend fun startPlan(id: String, remember: Boolean): TaskPlan = error("unused")
        override suspend fun dismissPlan(id: String): TaskPlan = error("unused")
        override suspend fun retryTask(todoId: String): Todo = error("unused")
        override suspend fun removeTask(todoId: String) = Unit
        override suspend fun route(conversation: String, todoId: String, text: String): FeedItem = error("unused")
        override suspend fun today(conversation: String): FeedItem = error("unused")
        override suspend fun mainConversation(): String? = null
        override suspend fun inboxReply(itemId: String, text: String, card: String?) = Unit
    }

    private class Connection : HubConnection {
        override val state = MutableStateFlow(ConnectionState.Online)
        override val events = MutableSharedFlow<ServerEvent>()
        override val lastHello: Hello get() = Hello(serverVersion = "1.0.0", protocol = 1)
        override fun reconnect() = Unit
        override fun send(message: JsonObject) = true
    }

    /** What the shell would show: failures, and notices with their button. */
    private class Notes : AppFeedback {
        val failures = mutableListOf<Throwable>()
        val notices = mutableListOf<Pair<String, FeedbackAction?>>()
        override fun failure(error: Throwable, operation: Boolean) = true.also { failures += error }
        override fun notice(message: String) = true.also { notices += message to null }
        override fun notice(message: String, action: FeedbackAction) = true.also { notices += message to action }
    }

    private val notes = Notes()
    private lateinit var store: TasksStore

    private fun open(api: Api) {
        store = TasksStore(api, Connection(), CoroutineScope(Dispatchers.Unconfined))
        store.reloadTodos()
        rule.setContent {
            PersonaTheme(reduceMotion = true) {
                CompositionLocalProvider(LocalAppFeedback provides notes) {
                    val todos by store.todos.collectAsState()
                    Box(Modifier.width(400.dp).height(860.dp)) {
                        TodosScreen(todos, emptyList(), store, onBack = {}, onOpenTodo = {}, now = now)
                    }
                }
            }
        }
        rule.waitForIdle()
        rule.onNodeWithText("日历").performClick()
        rule.waitForIdle()
    }

    private fun day(date: String) = rule.onNodeWithTag("todo-calendar-day-$date")

    /** Long-presses the first to-do drawn in [from], carries it to the middle of [to] (or keeps it there) and lets go. */
    private fun drag(from: String, to: String?) {
        val source = day(from)
        val start = source.fetchSemanticsNode().boundsInRoot
        val end = to?.let { day(it).fetchSemanticsNode().boundsInRoot }
        source.performTouchInput {
            // into the first to-do, under the date's circle
            val chip = Offset(centerX, (Tokens.Space.xs + CalendarTokens.dayMark + Tokens.Space.xxs + 6.dp).toPx())
            down(chip)
            move(delayMillis = viewConfiguration.longPressTimeoutMillis + 100)
            if (end != null) {
                val target = Offset(end.center.x - start.left, end.center.y - start.top)
                for (i in 1..4) moveTo(chip + (target - chip) * (i / 4f), delayMillis = 30)
            }
            up()
        }
        rule.waitForIdle()
    }

    private fun spoken(node: SemanticsNodeInteraction) =
        node.fetchSemanticsNode().config.getOrElse(androidx.compose.ui.semantics.SemanticsProperties.ContentDescription) { emptyList() }.joinToString()

    @Test fun aDropOnAnotherDayKeepsTheTimeAndUndoPutsItBack() {
        val api = Api(listOf(checkup, review))
        open(api)
        drag("2026-11-06", "2026-11-11")
        // 09:00 on Nov 11, in this zone
        assertEquals(listOf("t5" to TodoDue.At(Instant.parse("2026-11-11T01:00:00Z"))), api.sent)
        assertTrue(spoken(day("2026-11-11")).contains("体检预约"))
        assertTrue(!spoken(day("2026-11-06")).contains("体检预约"))
        val (message, undo) = notes.notices.single()
        assertEquals("已移到 11月11日周三 09:00", message)
        assertEquals("撤销", undo!!.label)
        undo.run()
        rule.waitForIdle()
        assertEquals("t5" to TodoDue.At(Instant.parse("2026-11-06T01:00:00Z")), api.sent.last())
        assertTrue(spoken(day("2026-11-06")).contains("体检预约"))
        // Undo says nothing more
        assertEquals(1, notes.notices.size)
    }

    @Test fun anAllDayOneStaysAllDay() {
        val api = Api(listOf(checkup, review))
        open(api)
        drag("2026-11-12", "2026-11-19")
        assertEquals(listOf("t6" to TodoDue.Day(LocalDate.of(2026, 11, 19))), api.sent)
        assertEquals("已移到 11月19日周四", notes.notices.single().first)
    }

    @Test fun droppedWhereItWasNothingIsSent() {
        val api = Api(listOf(checkup, review))
        open(api)
        drag("2026-11-06", null)
        drag("2026-11-06", "2026-11-06")
        assertTrue(api.sent.isEmpty())
        assertTrue(notes.notices.isEmpty())
        // and a held to-do let go opens nothing: the day's sheet comes from a tap
        assertTrue(rule.onAllNodesWithText("11月6日", substring = true).fetchSemanticsNodes().isEmpty())
    }

    @Test fun aFailedSavePutsItBackAndSaysWhy() {
        val api = Api(listOf(checkup, review)).apply { failure = IllegalStateException("offline") }
        open(api)
        drag("2026-11-06", "2026-11-11")
        assertEquals(1, api.sent.size)
        assertTrue(spoken(day("2026-11-06")).contains("体检预约"))
        assertTrue(!spoken(day("2026-11-11")).contains("体检预约"))
        assertEquals("offline", notes.failures.single().message)
        assertTrue(notes.notices.isEmpty())
    }

    @Test fun aSwipeStillTurnsTheMonthAndATapStillOpensTheDay() {
        val api = Api(listOf(checkup, review))
        open(api)
        // a swipe that starts on a to-do moves before a long press could pick it up
        day("2026-11-06").performTouchInput { swipeLeft(startX = centerX + 150f, endX = centerX - 150f) }
        rule.waitForIdle()
        assertTrue(rule.onAllNodesWithText("2026年12月").fetchSemanticsNodes().isNotEmpty())
        assertTrue(api.sent.isEmpty())
        day("2026-12-03").performClick()
        rule.waitForIdle()
        assertTrue(rule.onAllNodesWithText("12月3日", substring = true).fetchSemanticsNodes().isNotEmpty())
    }

    @Test fun talkBackChangesTheDueDateWithoutDragging() {
        val api = Api(listOf(checkup, review))
        open(api)
        // the day's element names an action for each of its to-dos
        val actions = day("2026-11-06").fetchSemanticsNode().config[SemanticsActions.CustomActions]
        assertEquals(listOf("改截止时间：体检预约…"), actions.map { it.label })
        rule.runOnIdle { actions.single().action() }
        rule.waitForIdle()
        rule.onNodeWithTag("todo-calendar-reschedule").assertExists()
        assertTrue(rule.onAllNodesWithText("改截止时间").fetchSemanticsNodes().isNotEmpty())
        // All day, on the day it was
        rule.onNodeWithText("全天").performClick()
        rule.onNodeWithTag("todo-calendar-reschedule-save").performClick()
        rule.waitForIdle()
        assertEquals(listOf("t5" to TodoDue.Day(LocalDate.of(2026, 11, 6))), api.sent)
        assertEquals("已移到 11月6日周五", notes.notices.single().first)

        // and from a row of the day's sheet
        day("2026-11-12").performClick()
        rule.waitForIdle()
        // the sheet's row, the one to-do of the 12th outside it (the month's day names its own)
        val rows = rule.onAllNodes(SemanticsMatcher.keyIsDefined(SemanticsActions.CustomActions)).fetchSemanticsNodes()
            .filter { node -> node.config[SemanticsActions.CustomActions].map { it.label } == listOf("改截止时间…") }
        assertEquals(1, rows.size)
        val rowActions = rows.single().config[SemanticsActions.CustomActions]
        rule.runOnIdle { rowActions.single().action() }
        rule.waitForIdle()
        rule.onNodeWithTag("todo-calendar-reschedule").assertExists()
        // a time for an all-day one: 18:00
        rule.onNodeWithText("全天").performClick()
        assertTrue(rule.onAllNodesWithText("18:00").fetchSemanticsNodes().isNotEmpty())
        rule.onNodeWithTag("todo-calendar-reschedule-save").performClick()
        rule.waitForIdle()
        assertEquals("t6" to TodoDue.At(Instant.parse("2026-11-12T10:00:00Z")), api.sent.last())
    }
}
