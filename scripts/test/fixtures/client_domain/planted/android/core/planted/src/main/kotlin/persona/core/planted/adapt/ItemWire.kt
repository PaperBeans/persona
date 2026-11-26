@file:OptIn(DomainBoundary::class)

package persona.core.planted.adapt

import kotlinx.serialization.Serializable
import persona.core.domainkit.DomainBoundary
import persona.core.domainkit.Instant
import persona.core.domainkit.Outcome
import persona.core.planted.domain.Item
import persona.core.planted.domain.ItemId
import persona.core.planted.domain.ItemVisitor
import persona.core.planted.domain.PlantedFail
import persona.core.planted.domain.Urgency

/** An item as the hub sends and stores it. */
@Serializable
internal data class ItemWire(val id: String, val urgent: Boolean, val wakesAt: Long)

/** Parses the wire item into the domain: the way in. */
internal fun ItemWire.toDomain(): Outcome<Item, PlantedFail> =
    ItemId.parse(id).fold(
        { Outcome.failed(it) },
        { Outcome.ok(Item.new(it, if (urgent) Urgency.urgent else Urgency.routine, Instant.parse(wakesAt))) },
    )

/** Visits the item for its wire shape: the way out. */
internal fun Item.toWire(): ItemWire = WireReader().also { accept(it) }.wire

private class WireReader : ItemVisitor {
    var wire: ItemWire = ItemWire("", false, 0)

    override fun visitItem(id: ItemId, urgency: Urgency, wakes: Instant) {
        wire = ItemWire(id.wire(), urgency.breaksQuiet, wakes.wire())
    }
}
