package persona.core.broken.domain

import java.util.UUID
import kotlinx.serialization.Serializable
import persona.core.domainkit.DomainBoundary
import persona.core.domainkit.Instant
import persona.core.domainkit.Now
import persona.core.planted.domain.ItemId

@Serializable
public data class Record(val doneAt: Long)

public sealed interface State

public enum class Kind { ONE, TWO }

public class Open(public val name: String)

public class Broken private constructor(private val at: Instant) {
    public var count: Int = 0

    public fun doneAt(): Instant = at

    public fun rename(name: String): Broken = this

    public fun stamp(): Broken {
        println(UUID.randomUUID())
        if (System.currentTimeMillis() < 0) throw IllegalStateException()
        return this
    }

    public fun evolve(state: State): Broken = this

    @OptIn(DomainBoundary::class)
    public fun leak(): Long = at.wire()

    public fun id(): String = "broken"

    public fun wakes(now: Now): Instant = at

    public operator fun get(item: ItemId): Instant = at

    @IgnorableReturnValue
    public fun touched(): Broken = this

    public fun whenDue(effect: (Broken) -> Unit): Unit = effect(this)

    public fun due(): Boolean = persona.core.domainkit.adapt.SystemClock().now().reached(at)

    public fun pick(options: List<Instant>): Broken = Broken(options.shuffled().first())

    public fun <T> carry(value: T): Broken = this

    @DomainBoundary
    public fun raw(): Long = 0

    public companion object {
        public fun fromWire(at: Instant): Broken = Broken(at)

        public fun blank(at: Instant): Broken = Broken(at)

        public fun new(a: Instant, b: Instant, c: Instant, d: Instant, e: Instant): Broken = Broken(a)
    }
}

public interface BrokenStore {
    public fun save(broken: Broken)
}

public abstract class Shape

public interface BrokenVisitor {
    public fun read(broken: Broken)
}

public object Registry {
    private val seen: MutableSet<Instant> = HashSet()
}

private val cache = mutableMapOf<Instant, Broken>()

/** Primitives beside other fields, a getter one step out, and factories that return null or another type. */
public class Entry private constructor(
    private val title: String,
    private val count: Int,
    internal val wakes: Instant,
) {
    private val total: Int = count

    public companion object {
        public fun wakesOf(entry: Entry): Instant = entry.wakes

        public fun from(wakes: Instant): Entry? = null

        public fun parse(raw: Long): Instant = Instant.parse(raw)

        public fun restore(wakes: Instant): Entry = Entry("", 0, wakes)
    }
}

public fun wakesAt(entry: Entry): Instant = entry.wakes

/** A value class: its toString prints the code. */
@JvmInline
public value class Code private constructor(private val code: Long)

/** A state beside another field: not a closed set, so it names no factory for a case. */
public class Phase private constructor(private val phase: Case) {
    private enum class Case { OPEN, DONE }

    private val since: Instant = Instant.parse(0)

    public companion object {
        public fun draft(): Phase = Phase(Case.OPEN)
    }
}

/** A primitive under another name beside another field, and a companion wire() and id() that hand a field out. */
internal typealias Raw = String

public class Label private constructor(private val raw: Raw, private val wakes: Instant) {
    public companion object {
        public fun wire(label: Label): Instant = label.wakes

        public fun id(label: Label): Instant = label.wakes
    }
}
