package persona.feature.settings

import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Test

/** A code step's asks while its page is shown, away for a while, and back (#4133). */
class SignInPollingTest {
    @Test fun asksWhileShownForAWhileAwayThenAtOnceOnReturn() = runTest {
        val shown = MutableStateFlow(true)
        var asks = 0
        var catchUps = 0
        backgroundScope.launch {
            pollWhileShown(shown, SignInAway(), awayMillis = 10_500, now = { testScheduler.currentTime }, catchUp = { catchUps++ }, poll = { while (true) { delay(2_000); asks++ } })
        }
        runCurrent()
        assertEquals(0, catchUps) // the first time shown is not a return
        advanceTimeBy(6_001)
        assertEquals(3, asks)

        // Stopped (the user went to scan with another app): the asks go on for the grace period, then stop.
        shown.value = false
        runCurrent()
        advanceTimeBy(10_001)
        assertEquals(3 + 5, asks)
        advanceTimeBy(60_000)
        assertEquals(8, asks)
        assertEquals(0, catchUps)

        // Back: one ask at once, then the usual interval again.
        shown.value = true
        runCurrent()
        assertEquals(1, catchUps)
        assertEquals(8, asks)
        advanceTimeBy(2_001)
        assertEquals(9, asks)

        // A short trip away (inside the grace period) still asks at once on return.
        shown.value = false
        runCurrent()
        advanceTimeBy(1_000)
        shown.value = true
        runCurrent()
        assertEquals(2, catchUps)

        // The same state again (RESUMED and STARTED both count as shown) changes nothing.
        shown.value = true
        runCurrent()
        assertEquals(2, catchUps)
    }

    /**
     * A new code while away (the hub made another WeChat QR code, so the page's effect starts again for it) does not
     * start the grace period again: the asks still stop five minutes after the page first left, and the return still
     * asks at once.
     */
    @Test fun aNewCodeWhileAwayKeepsTheOriginalDeadline() = runTest {
        val shown = MutableStateFlow(true)
        val away = SignInAway()
        var asks = 0
        var catchUps = 0
        fun effect() = backgroundScope.launch {
            pollWhileShown(shown, away, now = { testScheduler.currentTime }, catchUp = { catchUps++ },
                poll = { while (true) { delay(2_000); asks++ } })
        }
        var running = effect()
        runCurrent()
        shown.value = false // left at 0
        runCurrent()
        // Every 2 minutes away a new code comes: the effect for the old one goes, one for the new one starts.
        repeat(2) {
            advanceTimeBy(120_000)
            running.cancel()
            running = effect()
            runCurrent()
        }
        advanceTimeBy(60_001) // 5 minutes since the page left
        val atDeadline = asks
        assertEquals(59 + 59 + 29, atDeadline) // each new effect waits its first interval afresh
        advanceTimeBy(30 * 60_000)
        assertEquals(atDeadline, asks)
        running.cancel()
        running = effect()
        advanceTimeBy(30 * 60_000)
        assertEquals(atDeadline, asks) // a new code after the deadline asks nothing either

        shown.value = true
        runCurrent()
        assertEquals(1, catchUps)
        advanceTimeBy(2_001)
        assertEquals(atDeadline + 1, asks)
    }

    @Test fun firstSeenHiddenAsksForTheGracePeriodOnly() = runTest {
        val shown = MutableStateFlow(false)
        var asks = 0
        var catchUps = 0
        backgroundScope.launch {
            pollWhileShown(shown, SignInAway(), awayMillis = 4_500, now = { testScheduler.currentTime }, catchUp = { catchUps++ }, poll = { while (true) { delay(1_000); asks++ } })
        }
        advanceTimeBy(30_000)
        assertEquals(4, asks) // at 1, 2, 3 and 4 seconds; the grace period ends half way through the fifth wait
        shown.value = true
        runCurrent()
        assertEquals(1, catchUps)
    }
}
