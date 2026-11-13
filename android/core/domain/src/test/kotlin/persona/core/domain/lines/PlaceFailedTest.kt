package persona.core.domain.lines

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import persona.core.contracts.Item
import persona.core.contracts.SessionKey
import persona.core.contracts.api.DeliveryState
import persona.core.contracts.api.OutboxMessage
import java.time.Instant

class PlaceFailedTest {
    private val key = SessionKey("m1", "s1")
    private val at = Instant.parse("2026-01-02T03:04:05Z").toEpochMilli()

    private fun user(id: String, text: String, client: String? = null) =
        ChatLine.Row(ChatRow("user", LineItem(Item(id, 1, role = "user", kind = "text", text = text, ts = "2026-01-02T03:04:05Z", clientMsgId = client))))

    private fun reply(id: String, text: String) =
        ChatLine.Row(ChatRow("reply", LineItem(Item(id, 2, role = "assistant", kind = "text", text = text, ts = "2026-01-02T03:04:05Z"))))

    @Test fun aFailedSendStaysBeforeTheMessageWrittenAfterIt() {
        val failed = OutboxMessage("m1", key, "Find a dinner place.", DeliveryState.Refused, createdAt = at + 1_000)
        val lines = listOf(user("l1#0", "Plan a weekend trip."), reply("l2#0", "Saturday hike."), user("l4#0", "And a backup plan for rain.", "m2"))
        val placed = placeFailed(lines, listOf(failed), writtenAt = mapOf("m2" to at + 2_000))
        assertEquals(listOf("m1"), placed.before["l4#0"]?.map { it.clientMessageId })
        assertTrue(placed.rest.isEmpty())
    }

    @Test fun aMessageStillOnItsWayStaysAtTheEnd() {
        val sending = OutboxMessage("m3", key, "One more thing.", DeliveryState.Sending, createdAt = at + 3_000)
        val lines = listOf(user("l1#0", "Plan a weekend trip."))
        val placed = placeFailed(lines, listOf(sending), writtenAt = mapOf("m3" to at + 3_000))
        assertTrue(placed.before.isEmpty())
        assertEquals(listOf("m3"), placed.rest.map { it.clientMessageId })
    }
}
