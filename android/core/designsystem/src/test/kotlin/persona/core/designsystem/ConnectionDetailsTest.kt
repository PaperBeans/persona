package persona.core.designsystem

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import androidx.test.ext.junit.runners.AndroidJUnit4
import persona.core.contracts.CoreText
import persona.core.contracts.SessionKey
import persona.core.contracts.api.ConnectionDiagnostics
import persona.core.contracts.api.ConnectionState
import persona.core.contracts.api.DeliveryState
import persona.core.contracts.api.OutboxMessage
import kotlinx.coroutines.launch
import java.time.Instant
import java.time.ZoneId
import java.util.Locale

class ConnectionDetailsTest {
    private val now = Instant.parse("2026-11-05T10:00:00Z")
    private val diagnostics = ConnectionDiagnostics(
        lastDisconnectReason = "Timeout",
        lastAttemptFailure = CoreText.of(CoreText.Key.EndpointTimeout),
        nextRetryAt = now.plusMillis(6_200).toString(),
        lastReceivedAt = now.minusSeconds(95).toString(),
    )

    @Test fun connectingSaysWhyWhenNextAndSinceWhen() {
        assertEquals(listOf(
            ConnectionFact.LastFailure(CoreText.of(CoreText.Key.EndpointTimeout)),
            ConnectionFact.RetryIn(7),
            ConnectionFact.LastOnline(now.minusSeconds(95)),
            ConnectionFact.Waiting(2),
        ), connectionFacts(ConnectionState.Connecting, diagnostics, 2, now))
    }

    @Test fun aFailureFromBeforeTheLastConnectionIsNotShown() {
        // The raw reason settings keep stays; once a connection got through, the transport clears lastAttemptFailure.
        val facts = connectionFacts(ConnectionState.Connecting, diagnostics.copy(lastAttemptFailure = null), 0, now)
        assertTrue(facts.none { it is ConnectionFact.LastFailure })
    }

    @Test fun aPassedRetryTimeReadsAsRetrying() {
        val facts = connectionFacts(ConnectionState.Connecting, diagnostics.copy(nextRetryAt = now.minusSeconds(1).toString()), 0, now)
        assertTrue(ConnectionFact.RetryIn(0) in facts)
    }

    @Test fun onlyConnectingShowsTheFailureAndTheCountdown() {
        val waiting = connectionFacts(ConnectionState.WaitingForNetwork, diagnostics, 0, now)
        assertEquals(listOf(ConnectionFact.LastOnline(now.minusSeconds(95))), waiting)
        assertEquals(emptyList<ConnectionFact>(), connectionFacts(ConnectionState.Online, diagnostics, 0, now))
        assertEquals(listOf(ConnectionFact.Waiting(1)), connectionFacts(ConnectionState.Online, null, 1, now))
    }

    @Test fun unreadableTimesAreLeftOut() {
        val facts = connectionFacts(ConnectionState.Connecting, ConnectionDiagnostics(nextRetryAt = "soon", lastReceivedAt = ""), 0, now)
        assertEquals(emptyList<ConnectionFact>(), facts)
    }

    @Test fun onlyMessagesStillOnTheirWayCountAsWaiting() {
        val key = SessionKey("m", "s")
        val outbox = DeliveryState.entries.map { OutboxMessage(it.name, key, "x", it) }
        assertEquals(3, waitingMessages(outbox))
    }

    @Test fun retryIsOfferedOnlyWhereItHelps() {
        assertTrue(ConnectionState.Connecting.retryable)
        assertTrue(ConnectionState.WaitingForNetwork.retryable)
        assertTrue(ConnectionState.Updating.retryable)
        assertFalse(ConnectionState.Online.retryable)
        assertFalse(ConnectionState.Revoked.retryable)
        assertFalse(ConnectionState.CertificateMismatch.retryable)
        assertFalse(ConnectionState.NeedsLocalNetworkPermission.retryable)
    }

    @Test fun failuresBuzzAsErrorsOrWhileOfflineAsANudge() {
        var state = ConnectionState.Online
        var clock = 0L
        val gate = FeedbackGate({ state }, { clock }, localize = { it.toString() })
        val seen = mutableListOf<FeedbackNotice>()
        val collector = kotlinx.coroutines.CoroutineScope(kotlinx.coroutines.Dispatchers.Unconfined).launch { gate.notices.collect { seen += it } }
        gate.failure(IllegalStateException("boom"))
        clock += 5_000
        state = ConnectionState.WaitingForNetwork
        gate.failure(IllegalStateException("boom"))
        clock += 5_000
        gate.notice("plain")
        collector.cancel()
        // A failure is an error toast (its style plays the error haptic); one while offline is info with a nudge.
        assertEquals(listOf(Haptic.Error, Haptic.Attention, null), seen.map { it.haptic ?: it.style.haptic })
        assertEquals(listOf(FeedbackStyle.Error, FeedbackStyle.Info, FeedbackStyle.Info), seen.map { it.style })
    }
}

/** "Last online" past the first hour, in the app's language even when the system's is another (#4103). */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
class ConnectionTimeLanguageTest {
    private val zone = ZoneId.of("UTC")
    private val now = Instant.parse("2026-11-05T20:00:00Z")
    private val today = Instant.parse("2026-11-05T18:00:00Z")
    private val earlier = Instant.parse("2026-11-04T18:00:00Z")

    private fun withSystem(locale: Locale, block: () -> Unit) {
        val saved = Locale.getDefault()
        Locale.setDefault(locale)
        try { block() } finally { Locale.setDefault(saved) }
    }

    @Test fun japaneseAppOnAnEnglishPhone() = withSystem(Locale.US) {
        assertEquals("18:00", clockOrDate(today, now, Locale.JAPANESE, zone))
        assertEquals("2026年11月4日", clockOrDate(earlier, now, Locale.JAPANESE, zone))
    }

    @Test fun chineseAppOnAnEnglishPhone() = withSystem(Locale.US) {
        assertEquals("2026年11月4日", clockOrDate(earlier, now, Locale.SIMPLIFIED_CHINESE, zone))
        assertFalse(clockOrDate(today, now, Locale.SIMPLIFIED_CHINESE, zone).contains("PM"))
    }

    @Test fun englishAppOnAJapanesePhone() = withSystem(Locale.JAPAN) {
        assertEquals("Nov 4, 2026", clockOrDate(earlier, now, Locale.ENGLISH, zone))
        val time = clockOrDate(today, now, Locale.US, zone)
        assertTrue(time, time.startsWith("6:00") && time.endsWith("PM"))
    }
}
