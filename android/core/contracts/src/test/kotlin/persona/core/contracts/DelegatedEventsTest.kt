package persona.core.contracts

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The answer fields of `interaction_resolved` and `interaction_reanswered` (docs/protocol/protocol.md, iOS
 * `DelegateTests.theResolvedEventCarriesTheAssistantsAnswer`); the shared samples are in `spec/fixtures/wire/interactions.json`.
 */
class DelegatedEventsTest {
    @Test fun theResolvedEventCarriesTheAssistantsAnswer() {
        val answered = ServerEvent.decode("""{"type":"interaction_resolved","id":"ix_1","answered_by":"assistant","reason":"The code uses pgx","answers":{"Which driver?":"pgx","n":3}}""")
            as InteractionResolvedEvent
        assertEquals("ix_1", answered.id)
        assertTrue(answered.byAssistant)
        assertEquals("The code uses pgx", answered.reason)
        // A value that is not text drops alone, as iOS reads it.
        assertEquals(mapOf("Which driver?" to "pgx"), answered.answers)

        val plain = ServerEvent.decode("""{"type":"interaction_resolved","id":"ix_2"}""") as InteractionResolvedEvent
        assertFalse(plain.byAssistant)
        assertEquals(emptyMap<String, String>(), plain.answers)
        // Answers of an odd shape never cost the withdrawal.
        val odd = ServerEvent.decode("""{"type":"interaction_resolved","id":"ix_3","answered_by":"assistant","answers":["pgx"]}""") as InteractionResolvedEvent
        assertEquals("ix_3", odd.id)
        assertEquals(emptyMap<String, String>(), odd.answers)
    }

    @Test fun theReansweredEventNamesTheQuestionsThatChanged() {
        val changed = ServerEvent.decode("""{"type":"interaction_reanswered","id":"ix_1","resolution":{"answers":{"Which driver?":"sqlc"}}}""")
            as InteractionReansweredEvent
        assertEquals("ix_1", changed.id)
        assertEquals(mapOf("Which driver?" to "sqlc"), changed.answers)
        assertEquals(emptyMap<String, String>(), (ServerEvent.decode("""{"type":"interaction_reanswered","id":"ix_2"}""") as InteractionReansweredEvent).answers)
        assertEquals(null, ServerEvent.decode("""{"type":"interaction_reanswered"}"""))
        // Sent back out the way it came, for the socket's replays and the tests.
        assertEquals(changed, ServerEvent.decode(JsonCodec.encodeToString(ServerEvent.serializer(), changed)))
    }
}
