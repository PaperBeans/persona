package persona.core.domain.lines

import persona.core.contracts.Item
import persona.core.contracts.MessageEcho
import persona.core.contracts.api.DeliveryState
import persona.core.contracts.api.OutboxMessage
import java.time.Instant

/**
 * Where a failed send is drawn (#2769), the same rule as `ChatLines.placeFailed` on iOS.
 *
 * A message that failed goes immediately before the first later line (a message sent after it). One with nothing
 * after it, one without a time, and every message still on its way stay at the end, in the order they were written.
 * [writtenAt] is when this phone wrote a send, keyed by client message id; a line typed elsewhere uses the computer's
 * time minus [MessageEcho.CLOCK_SLACK_MILLIS], so a near tie leaves the failed message below.
 */
fun placeFailed(
    lines: List<ChatLine>,
    pending: List<OutboxMessage>,
    writtenAt: Map<String, Long> = emptyMap(),
): PlacedMessages {
    val ordered = pending.withIndex().sortedWith(
        compareBy<IndexedValue<OutboxMessage>> { it.value.createdAt ?: Long.MIN_VALUE }.thenBy { it.index },
    ).map { it.value }
    if (ordered.none { it.state in failedDelivery }) return PlacedMessages(emptyMap(), ordered)
    val after = lines.mapNotNull { line -> placeTime(line, writtenAt)?.let { line.id to it } }
    val before = linkedMapOf<String, MutableList<OutboxMessage>>()
    val rest = mutableListOf<OutboxMessage>()
    for (message in ordered) {
        val at = message.createdAt
        val next = if (message.state in failedDelivery && at != null) after.firstOrNull { it.second > at } else null
        if (next == null) rest += message
        else before.getOrPut(next.first) { mutableListOf() } += message
    }
    return PlacedMessages(before, rest)
}

data class PlacedMessages(
    val before: Map<String, List<OutboxMessage>>,
    val rest: List<OutboxMessage>,
)

private val failedDelivery = setOf(DeliveryState.Refused, DeliveryState.Rejected, DeliveryState.NeedsConfirmation)

private fun placeTime(line: ChatLine, writtenAt: Map<String, Long>): Long? {
    fun sent(item: Item): Long? {
        val id = item.clientMsgId?.takeIf { it.isNotEmpty() }
        if (id != null) writtenAt[id]?.let { return it }
        val at = item.ts?.let { runCatching { Instant.parse(it).toEpochMilli() }.getOrNull() } ?: return null
        return at - MessageEcho.CLOCK_SLACK_MILLIS
    }
    return when (line) {
        is ChatLine.Row -> if (line.row.type == "user" || (line.row.type == "image" && line.row.item?.item?.role == "user"))
            line.row.item?.item?.let(::sent) else null
        is ChatLine.UserImages -> line.images.firstNotNullOfOrNull { sent(it.item) }
        else -> null
    }
}
