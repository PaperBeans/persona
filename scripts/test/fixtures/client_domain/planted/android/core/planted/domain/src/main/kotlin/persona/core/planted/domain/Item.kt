package persona.core.planted.domain

import persona.core.domainkit.DomainBoundary
import persona.core.domainkit.Fail
import persona.core.domainkit.FailCode
import persona.core.domainkit.FailPart
import persona.core.domainkit.FailVisitor
import persona.core.domainkit.Instant
import persona.core.domainkit.Now
import persona.core.domainkit.Outcome

/**
 * An item's identity. Raw input becomes one only through parse; the adapter alone reads it back (wire). A class, not a
 * value class, so its toString prints no id.
 */
public class ItemId private constructor(private val item: String) {
    @DomainBoundary public fun wire(): String = item

    override fun equals(other: Any?): Boolean = other is ItemId && other.item == item

    override fun hashCode(): Int = item.hashCode()

    public companion object {
        public fun parse(raw: String): Outcome<ItemId, PlantedFail> =
            if (raw.isBlank()) Outcome.failed(PlantedFail.emptyId) else Outcome.ok(ItemId(raw))
    }
}

/** How urgent an item is: a closed set, as a class over a private enum, so no caller switches on it. */
public class Urgency private constructor(private val urgency: Case) {
    private enum class Case { ROUTINE, URGENT }

    /** Whether it may interrupt the owner's quiet hours. */
    public val breaksQuiet: Boolean get() = urgency == Case.URGENT

    public companion object {
        public val routine: Urgency = Urgency(Case.ROUTINE)
        public val urgent: Urgency = Urgency(Case.URGENT)
    }
}

/** The planted context's expected failures. */
public class PlantedFail private constructor(private val fail: Case) : Fail {
    internal sealed interface Case {
        object EmptyId : Case
        object AlreadyDue : Case
    }

    override fun accept(v: FailVisitor) {
        when (fail) {
            Case.EmptyId -> v.visitInvalid(FailCode.parse("planted.item.empty_id"), FailPart.parse("id"))
            Case.AlreadyDue -> v.visitRefused(FailCode.parse("planted.item.already_due"))
        }
    }

    public companion object {
        public val emptyId: PlantedFail = PlantedFail(Case.EmptyId)
        public val alreadyDue: PlantedFail = PlantedFail(Case.AlreadyDue)
    }
}

/** Reads an item for an adapter: what it is, how urgent, and when it comes back to the owner. */
public interface ItemVisitor {
    public fun visitItem(id: ItemId, urgency: Urgency, wakes: Instant)
}

/**
 * An item the owner may have to look at. It never throws, prints (println(...)) or reads the clock: a snooze takes Now
 * from its caller and returns the next item, or why it was refused.
 */
public class Item private constructor(
    private val identity: ItemId,
    private val urgency: Urgency,
    private val wakes: Instant,
) {
    public fun id(): ItemId = identity

    public fun isSame(other: Item): Boolean = other.identity == identity

    public fun needsOwner(now: Now): Boolean = urgency.breaksQuiet || now.reached(wakes)

    public fun snoozed(until: Instant, now: Now): Outcome<Item, PlantedFail> =
        if (now.reached(until)) Outcome.failed(PlantedFail.alreadyDue) else Outcome.ok(Item(identity, urgency, until))

    public fun accept(v: ItemVisitor) {
        v.visitItem(identity, urgency, wakes)
    }

    override fun equals(other: Any?): Boolean = other is Item && other.identity == identity

    override fun hashCode(): Int = identity.hashCode()

    public companion object {
        public fun new(id: ItemId, urgency: Urgency, arrived: Instant): Item = Item(id, urgency, arrived)
    }
}

/**
 * When an item comes back to the owner: at an instant, or never. A closed set whose case with data has a factory named
 * for the case.
 */
public class Wake private constructor(private val wake: Case) {
    internal sealed interface Case {
        class At(val instant: Instant) : Case
        object Never : Case
    }

    public fun reached(now: Now): Boolean =
        when (val case = wake) {
            is Case.At -> now.reached(case.instant)
            Case.Never -> false
        }

    public companion object {
        public val never: Wake = Wake(Case.Never)

        public fun at(instant: Instant): Wake = Wake(Case.At(instant))
    }
}

/**
 * When a snoozed item comes back to the owner: not again if it already needs them, else when the snooze ends. A domain
 * service: it asks the item, and never reads its fields.
 */
public fun nextWake(item: Item, until: Instant, now: Now): Wake =
    if (item.needsOwner(now)) Wake.never else Wake.at(until)
