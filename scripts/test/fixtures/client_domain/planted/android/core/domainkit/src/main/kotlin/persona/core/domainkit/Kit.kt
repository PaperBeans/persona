package persona.core.domainkit

/** A boundary conversion: only adapters opt in. */
@RequiresOptIn(message = "Boundary conversion: adapters only", level = RequiresOptIn.Level.ERROR)
@Retention(AnnotationRetention.BINARY)
public annotation class DomainBoundary

/** Reading the clock: only the kit's clock adapter opts in. */
@RequiresOptIn(message = "Reads the clock: the kit's clock adapter only", level = RequiresOptIn.Level.ERROR)
@Retention(AnnotationRetention.BINARY)
public annotation class ClockRead

/** An expected failure, read only by visiting it. Not a Throwable. */
public interface Fail {
    public fun accept(v: FailVisitor)
}

/** Reads a failure by its kind. Adapters implement it to map a failure once, at the edge. */
public interface FailVisitor {
    public fun visitInvalid(code: FailCode, part: FailPart)
    public fun visitRefused(code: FailCode)
}

/** A failure's code. */
public class FailCode private constructor(private val code: String) {
    @DomainBoundary public fun wire(): String = code

    public companion object {
        public fun parse(code: String): FailCode = FailCode(code)
    }
}

/** The wrong input, in the core's own words. */
public class FailPart private constructor(private val part: String) {
    @DomainBoundary public fun wire(): String = part

    public companion object {
        public fun parse(part: String): FailPart = FailPart(part)
    }
}

/** A value or a failure of one family, read through fold: a closed set whose two cases carry their data. */
public class Outcome<out T, out F : Fail> private constructor(private val outcome: Case<T, F>) {
    internal sealed interface Case<out T, out F> {
        class Ok<out T>(val value: T) : Case<T, Nothing>
        class Failed<out F>(val failure: F) : Case<Nothing, F>
    }

    public fun <R> fold(onFail: (F) -> R, onOk: (T) -> R): R =
        when (val case = outcome) {
            is Case.Ok -> onOk(case.value)
            is Case.Failed -> onFail(case.failure)
        }

    public companion object {
        public fun <T> ok(value: T): Outcome<T, Nothing> = Outcome(Case.Ok(value))
        public fun <F : Fail> failed(failure: F): Outcome<Nothing, F> = Outcome(Case.Failed(failure))
    }
}

/** A point in time, in UTC nanoseconds. A class, not a value class, so its toString prints no nanoseconds. */
public class Instant private constructor(private val instant: Long) {
    public fun isBefore(other: Instant): Boolean = instant < other.instant

    @DomainBoundary public fun wire(): Long = instant

    override fun equals(other: Any?): Boolean = other is Instant && other.instant == instant

    override fun hashCode(): Int = instant.hashCode()

    public companion object {
        public fun parse(epochNanos: Long): Instant = Instant(epochNanos)
    }
}

/** The application's reading of the clock. Only the kit's clock adapter makes one, so a core never reads the time. */
public class Now private constructor(private val now: Instant) {
    public fun reached(deadline: Instant): Boolean = !now.isBefore(deadline)

    public companion object {
        @ClockRead public fun read(at: Instant): Now = Now(at)
    }
}
