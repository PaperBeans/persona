@file:OptIn(ClockRead::class)

package persona.core.domainkit.adapt

import persona.core.domainkit.ClockRead
import persona.core.domainkit.Instant
import persona.core.domainkit.Now

/** Reads the system clock once per call: the application's maker of Now. */
public class SystemClock {
    public fun now(): Now = Now.read(Instant.parse(System.currentTimeMillis() * 1_000_000))
}

/** A clock stopped at one instant, for tests. */
public class FixedClock(private val epochNanos: Long) {
    public fun now(): Now = Now.read(Instant.parse(epochNanos))
}
