package persona.feature.planted

import persona.core.domainkit.DomainBoundary
import persona.core.domainkit.Instant
import persona.core.domainkit.Now
import persona.core.planted.domain.Item
import persona.core.planted.domain.ItemId
import persona.core.planted.domain.ItemVisitor
import persona.core.planted.domain.Urgency

// App code breaking the rules outside the domain modules. expected.txt lists the findings.
@OptIn(DomainBoundary::class)
internal fun leak(id: ItemId): String = id.wire()

internal class RowReader : ItemVisitor {
    override fun visitItem(id: ItemId, urgency: Urgency, wakes: Instant) = Unit
}

internal fun drop(item: Item, until: Instant, now: Now) {
    val _ = item.snoozed(until, now)
    val _ = runCatching { item.snoozed(until, now) }
}
