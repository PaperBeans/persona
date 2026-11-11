package persona.feature.settings

import androidx.lifecycle.Lifecycle
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.withTimeoutOrNull

/**
 * How long a code step (a QR code, a device or pairing code) goes on asking the hub after its page stopped: another tab,
 * the app in the background, the screen off. These steps send the user to another app (scan with WeChat, approve in a
 * browser), and for WeChat the hub finishes the binding only inside an ask, so the asks go on long enough for that trip.
 * Counted from when the page left, once per sign-in: the hub's own clock on a WeChat QR code (5 minutes from when the
 * code was made, then a new one) is separate, and a new code made meanwhile does not start this one again. After it,
 * nothing until the page is back.
 */
internal const val SignInAwayMillis = 5 * 60_000L

/**
 * When the page left, for one sign-in: kept by the page outside the effect that polls (`remember`), so the effect starting
 * again for a new code (another QR code, keyed on its URL) neither restarts the grace period nor forgets the return.
 */
internal class SignInAway {
    /** Milliseconds on [pollWhileShown]'s clock when the page stopped; null while it is shown. */
    var since: Long? = null
}

/**
 * Asks about the code on screen while [shown]: [poll] as usual, and still until [awayMillis] after the page stopped
 * ([away] remembers when). Back in front after being away, [catchUp] first (one ask at once, so a sign-in finished
 * elsewhere shows without waiting an interval), then [poll] again. One ask at a time: each change of [shown] cancels the
 * one before.
 */
internal suspend fun pollWhileShown(
    shown: Flow<Boolean>, away: SignInAway, awayMillis: Long = SignInAwayMillis,
    now: () -> Long = { System.nanoTime() / 1_000_000 },
    catchUp: suspend () -> Unit, poll: suspend () -> Unit,
) {
    shown.distinctUntilChanged().collectLatest { front ->
        if (front) {
            if (away.since != null) catchUp()
            away.since = null
            poll()
        } else {
            val since = away.since ?: now().also { away.since = it }
            val left = since + awayMillis - now()
            if (left > 0) withTimeoutOrNull(left) { poll() }
        }
    }
}

/** A page is shown while its lifecycle is at least STARTED (a hidden shell tab is held at CREATED). */
internal fun Lifecycle.startedFlow(): Flow<Boolean> = currentStateFlow.map { it.isAtLeast(Lifecycle.State.STARTED) }
