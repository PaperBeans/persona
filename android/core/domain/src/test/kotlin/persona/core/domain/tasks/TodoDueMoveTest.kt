package persona.core.domain.tasks

import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.async
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.JsonObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import persona.core.contracts.api.ConnectionState
import persona.core.contracts.FeedItem
import persona.core.contracts.FeedResponse
import persona.core.contracts.ServerEvent
import persona.core.contracts.TaskPlan
import persona.core.contracts.Todo
import persona.core.contracts.TodoDue
import persona.core.contracts.api.HubConnection
import persona.core.contracts.api.TasksApi
import java.time.Instant
import java.time.LocalDate

// A to-do moved on the calendar (#2045): drawn there at once, the hub's answer kept, a failed save put back alone.
class TodoDueMoveTest {
    private class Connection : HubConnection {
        override val state = MutableStateFlow(ConnectionState.WaitingForNetwork)
        override val events = MutableSharedFlow<ServerEvent>()
        override fun reconnect() = Unit
        override fun send(message: JsonObject) = true
    }

    private class Api(var rows: List<Todo>) : TasksApi {
        val sent = mutableListOf<Pair<String, TodoDue>>()
        var gate: CompletableDeferred<Unit>? = null
        var failure: Throwable? = null
        override suspend fun setTodoDue(id: String, due: TodoDue): Todo {
            sent += id to due
            gate?.await()
            failure?.let { throw it }
            // the hub's answer carries what it changed besides (here: its updated time)
            val updated = rows.first { it.id == id }.withDue(due).copy(updatedAt = "2026-11-05T09:31:00Z")
            rows = rows.map { if (it.id == id) updated else it }
            return updated
        }
        override suspend fun todos(): List<Todo> = rows
        override suspend fun feed(conversation: String, since: String?) = FeedResponse()
        override suspend fun startPlan(id: String, remember: Boolean): TaskPlan = error("unused")
        override suspend fun dismissPlan(id: String): TaskPlan = error("unused")
        override suspend fun retryTask(todoId: String): Todo = error("unused")
        override suspend fun removeTask(todoId: String) = Unit
        override suspend fun route(conversation: String, todoId: String, text: String): FeedItem = error("unused")
        override suspend fun today(conversation: String): FeedItem = error("unused")
        override suspend fun mainConversation(): String? = null
        override suspend fun inboxReply(itemId: String, text: String, card: String?) = Unit
    }

    private val flight = Todo("t9", title = "Book the flight", dueAt = "2026-11-12T10:00:00Z", remindAt = "2026-11-12T04:00:00Z")
    private val rent = Todo("t11", title = "Pay the rent", dueDate = "2026-11-18")
    private val later = TodoDue.At(Instant.parse("2026-11-19T10:00:00Z"))

    @Test fun drawnThereAtOnceThenTheHubsAnswer() = runTest {
        val api = Api(listOf(flight, rent)).apply { gate = CompletableDeferred() }
        val store = TasksStore(api, Connection(), backgroundScope)
        store.reloadTodos(); runCurrent()
        val saving = async { store.setTodoDue(flight.id, later) }
        runCurrent()
        // moved before the hub answers, the reminder and the rest as they were
        val shown = store.todos.value.first { it.id == flight.id }
        assertEquals("2026-11-19T10:00:00Z", shown.dueAt)
        assertNull(shown.dueDate)
        assertEquals(flight.remindAt, shown.remindAt)
        api.gate!!.complete(Unit); runCurrent()
        assertEquals("2026-11-05T09:31:00Z", saving.await().updatedAt)
        assertEquals(listOf(flight.id to later), api.sent)
        assertEquals("2026-11-05T09:31:00Z", store.todos.value.first { it.id == flight.id }.updatedAt)
        assertEquals(rent, store.todos.value.first { it.id == rent.id })
    }

    @Test fun aFailedSavePutsBackOnlyThatOne() = runTest {
        val api = Api(listOf(flight, rent)).apply { gate = CompletableDeferred(); failure = IllegalStateException("synthetic failure") }
        val store = TasksStore(api, Connection(), backgroundScope)
        store.reloadTodos(); runCurrent()
        val saving = async { runCatching { store.setTodoDue(flight.id, later) } }
        val allDay = async { runCatching { store.setTodoDue(rent.id, TodoDue.Day(LocalDate.of(2026, 11, 20))) } }
        runCurrent()
        assertEquals("2026-11-20", store.todos.value.first { it.id == rent.id }.dueDate)
        api.gate!!.complete(Unit); runCurrent()
        assertEquals("synthetic failure", saving.await().exceptionOrNull()?.message)
        assertTrue(allDay.await().isFailure)
        assertEquals(listOf(flight, rent), store.todos.value)
    }

    @Test fun aFailedSaveKeepsWhatChangedItSince() = runTest {
        val api = Api(listOf(flight)).apply { gate = CompletableDeferred(); failure = IllegalStateException("synthetic failure") }
        val store = TasksStore(api, Connection(), backgroundScope)
        store.reloadTodos(); runCurrent()
        val saving = async { runCatching { store.setTodoDue(flight.id, later) } }
        runCurrent()
        // another device renamed it meanwhile; the next read brings that
        api.rows = listOf(flight.copy(title = "Book the flights"))
        store.reloadTodos(); runCurrent()
        api.gate!!.complete(Unit); runCurrent()
        assertTrue(saving.await().isFailure)
        assertEquals("Book the flights", store.todos.value.single().title)
    }
}
