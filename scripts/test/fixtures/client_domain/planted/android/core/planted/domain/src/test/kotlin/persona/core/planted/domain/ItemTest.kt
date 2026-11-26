package persona.core.planted.domain

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Test
import persona.core.domainkit.Instant
import persona.core.domainkit.adapt.FixedClock

class ItemTest {
    /** The test's own visitor: it keeps when the item wakes. */
    private class Wakes : ItemVisitor {
        var wakes: Instant? = null

        override fun visitItem(id: ItemId, urgency: Urgency, wakes: Instant) {
            this.wakes = wakes
        }
    }

    @Test
    fun snoozeMovesWhenTheItemWakes() {
        val now = FixedClock(100).now()
        val id = ItemId.parse("item-1").fold({ error("parse failed") }, { it })
        val item = Item.new(id, Urgency.routine, Instant.parse(50))
        val snoozed = item.snoozed(Instant.parse(200), now).fold({ error("snooze refused") }, { it })
        val reader = Wakes()
        snoozed.accept(reader)
        assertEquals(Instant.parse(200), reader.wakes)
        assertFalse(snoozed.needsOwner(now))
    }
}
