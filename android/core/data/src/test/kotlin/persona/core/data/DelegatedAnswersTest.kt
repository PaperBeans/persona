package persona.core.data

import persona.core.contracts.*
import persona.core.contracts.api.*
import persona.core.domain.answer.Delegated
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import org.junit.Assert.*
import org.junit.Test
import java.time.Instant

/**
 * The questions the assistant answered, kept per chat in the data layer (iOS `DelegateTests`' app cases): which are kept
 * and for how long, the hub's `already` reply, Change answer and `interaction_reanswered` (#4339 C9).
 */
@OptIn(ExperimentalCoroutinesApi::class)
class DelegatedAnswersTest {
    private val two = listOf(Question("Which driver?", options = listOf(QuestionOption("pgx"), QuestionOption("sqlc"))),
        Question("Test first?", options = listOf(QuestionOption("Yes"), QuestionOption("No"))))
    private fun card(id: String = "ix_1", session: String = "a", kind: String = "question") =
        Interaction(id, "mac", session, kind = kind, questions = two.takeIf { kind == "question" }, manager = "work")
    private fun resolved(id: String, answers: Map<String, String> = mapOf("Which driver?" to "pgx"), by: String? = "assistant", reason: String? = "The code uses pgx") =
        InteractionResolvedEvent(id, by, reason, JsonObject(answers.mapValues { JsonPrimitive(it.value) }))

    private class Fixture(scope: TestScope, pending: List<Interaction>) {
        val connection = Connection()
        val transport = Transport(pending)
        val center = HubInteractionCenter(transport, connection, scope.backgroundScope, clock = { Instant.parse("2026-11-12T08:00:00Z") })
    }

    private suspend fun TestScope.fixture(vararg pending: Interaction): Fixture =
        Fixture(this, pending.toList()).also { it.center.refresh(); runCurrent() }

    @Test fun aQuestionTheAssistantAnsweredStaysInItsChat() = runTest {
        val f = fixture(card(), card("ix_2"), card("ix_3", session = "b"), card("ix_4", kind = "permission"))
        f.connection.events.emit(resolved("ix_1"))
        runCurrent()
        assertTrue(f.center.pending.value.none { it.id == "ix_1" })
        val kept = f.center.delegated.value.single()
        assertEquals("ix_1", kept.id)
        assertEquals(mapOf("Which driver?" to "pgx"), kept.answers)
        assertEquals("The code uses pgx", kept.reason)
        assertEquals(Instant.parse("2026-11-12T08:00:00Z"), kept.at)
        assertEquals(two, kept.interaction.questions)

        // Answered by anyone else, a card that is no question, or one this device never had: nothing is kept.
        f.connection.events.emit(resolved("ix_2", by = null))
        f.connection.events.emit(resolved("ix_4"))
        f.connection.events.emit(resolved("ix_unknown"))
        runCurrent()
        assertEquals(listOf("ix_1"), f.center.delegated.value.map { it.id })

        // Another chat's answer is its own; the newest for a chat is the last.
        f.connection.events.emit(resolved("ix_3"))
        runCurrent()
        assertEquals(listOf("ix_1", "ix_3"), f.center.delegated.value.map { it.id })
        assertEquals("ix_3", f.center.delegated.value.last { it.interaction.sessionKey == SessionKey("mac", "b") }.id)

        // Put away; the other stays.
        f.center.dropDelegated("ix_1")
        assertEquals(listOf("ix_3"), f.center.delegated.value.map { it.id })
    }

    @Test fun atMostFiftyAreKeptTheOldestPutAwayFirst() = runTest {
        val cards = (0..Delegated.KEPT).map { card("ix_$it") }
        val f = fixture(*cards.toTypedArray())
        cards.forEach { f.connection.events.emit(resolved(it.id)) }
        runCurrent()
        val kept = f.center.delegated.value
        assertEquals(Delegated.KEPT, kept.size)
        assertEquals("ix_1", kept.first().id)
        assertEquals("ix_${Delegated.KEPT}", kept.last().id)
    }

    @Test fun thisDevicesAnswerThatCameSecondShowsTheAssistants() = runTest {
        val f = fixture(card())
        f.transport.outcome = ResolveOutcome(already = true, status = "resolved",
            resolution = Resolution(answers = mapOf("Which driver?" to "pgx"), answeredBy = "assistant", reason = "The code uses pgx"))
        f.center.resolveOutcome("ix_1", Resolution(answers = mapOf("Which driver?" to "sqlc")))
        runCurrent()
        assertEquals(listOf(mapOf("Which driver?" to "pgx")), f.center.delegated.value.map { it.answers })
        // Its event too, after the reply: still one.
        f.connection.events.emit(resolved("ix_1"))
        runCurrent()
        assertEquals(1, f.center.delegated.value.size)
    }

    @Test fun anAnswerThatTookEffectFromSomeoneElseKeepsNothing() = runTest {
        val f = fixture(card())
        f.transport.outcome = ResolveOutcome(already = true, status = "resolved", resolution = Resolution(answers = mapOf("Which driver?" to "pgx")))
        f.center.resolveOutcome("ix_1", Resolution(answers = mapOf("Which driver?" to "sqlc")))
        runCurrent()
        assertTrue(f.center.delegated.value.isEmpty())
    }

    @Test fun changeAnswerSendsWhatChangedAndShowsIt() = runTest {
        val f = fixture(card())
        f.connection.events.emit(resolved("ix_1", answers = mapOf("Which driver?" to "pgx", "Test first?" to "Yes")))
        runCurrent()
        val d = f.center.delegated.value.single()

        f.center.reanswer(d.id, mapOf("Which driver?" to "sqlc"), d.interaction.manager)
        assertEquals(listOf(Triple("ix_1", mapOf("Which driver?" to "sqlc"), "work")), f.transport.reanswers)
        assertEquals(mapOf("Which driver?" to "sqlc"), f.center.reanswered.value["ix_1"])
        assertEquals(mapOf("Which driver?" to "sqlc", "Test first?" to "Yes"), Delegated.latest(d, f.center.reanswered.value[d.id]))

        // Another device changes the other question: the hub names only that one, and the first change stays.
        f.connection.events.emit(InteractionReansweredEvent("ix_1", JsonObject(mapOf("answers" to JsonObject(mapOf("Test first?" to JsonPrimitive("No")))))))
        runCurrent()
        assertEquals(mapOf("Which driver?" to "sqlc", "Test first?" to "No"), f.center.reanswered.value["ix_1"])
        // Put away, what was changed stays for Session info.
        f.center.dropDelegated("ix_1")
        assertEquals(mapOf("Which driver?" to "sqlc", "Test first?" to "No"), f.center.reanswered.value["ix_1"])
    }

    @Test fun aChangeOfAnswerThatFailedChangesNothing() = runTest {
        val f = fixture(card())
        f.connection.events.emit(resolved("ix_1"))
        runCurrent()
        f.transport.refusal = IllegalStateException("assistant offline")
        assertTrue(runCatching { f.center.reanswer("ix_1", mapOf("Which driver?" to "sqlc"), null) }.isFailure)
        assertNull(f.center.reanswered.value["ix_1"])
        // A change that names no answer is no change.
        f.connection.events.emit(InteractionReansweredEvent("ix_1"))
        runCurrent()
        assertNull(f.center.reanswered.value["ix_1"])
    }

    /** Offline, so nothing refreshes unless the test asks. */
    private class Connection : HubConnection {
        override val state = MutableStateFlow(ConnectionState.WaitingForNetwork)
        override val events = MutableSharedFlow<ServerEvent>()
        override fun reconnect() = Unit
        override fun send(message: JsonObject): Boolean = true
    }

    private class Transport(var pending: List<Interaction>) : ChatTransport {
        var outcome = ResolveOutcome()
        var refusal: Exception? = null
        val reanswers = mutableListOf<Triple<String, Map<String, String>, String?>>()
        override suspend fun machines() = listOf(Machine("mac"))
        override suspend fun sessions() = emptyList<Session>()
        override suspend fun items(session: SessionKey, query: ItemsQuery): ItemsResponse = error("Unexpected items request")
        override suspend fun interactions() = pending
        override suspend fun resolve(id: String, resolution: Resolution) = Unit
        override suspend fun resolveOutcome(id: String, resolution: Resolution) = outcome
        override suspend fun reanswer(id: String, answers: Map<String, String>, manager: String?) {
            refusal?.let { throw it }
            reanswers += Triple(id, answers, manager)
        }
        override suspend fun image(session: SessionKey, itemId: String) = byteArrayOf()
        override suspend fun fullItem(session: SessionKey, itemId: String) = Item(itemId)
    }
}
