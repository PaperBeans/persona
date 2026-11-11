package persona.platform.notify

import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.serialization.json.JsonObject
import org.junit.Assert.assertEquals
import org.junit.Test
import persona.core.contracts.api.ConnectionState
import persona.core.contracts.api.GroupLink
import persona.core.contracts.api.HubConnection
import persona.core.contracts.ServerEvent

class ResidentConnectionTest {
    private class CountingConnection(state: ConnectionState) : HubConnection {
        override val state = MutableStateFlow(state)
        override val events = MutableSharedFlow<ServerEvent>()
        var reconnects = 0
        override fun reconnect() { reconnects++ }
        override fun send(message: JsonObject) = false
    }

    /** Two groups, the first selected; [reconnect] restarts both, as the app runtime does. */
    private class TwoGroupConnection(selected: ConnectionState, other: ConnectionState) : HubConnection {
        private val selectedState = MutableStateFlow(selected)
        private val otherState = MutableStateFlow(other)
        override val state = selectedState
        override val events = MutableSharedFlow<ServerEvent>()
        var selectedReconnects = 0
        var otherReconnects = 0
        var selectedWakes = 0
        var otherWakes = 0
        override fun reconnect() { selectedReconnects++; otherReconnects++ }
        override val groupLinks = listOf(
            GroupLink(selectedState, { selectedReconnects++ }, { selectedWakes++ }),
            GroupLink(otherState, { otherReconnects++ }, { otherWakes++ }),
        )
        override fun send(message: JsonObject) = false
    }

    // Starting the service (boot, the app coming to the front) dropped a live connection: "Connecting" flashed and an
    // event resume in flight started over (#4139).
    @Test fun aLiveConnectionIsLeftAlone() {
        for (state in listOf(ConnectionState.Online, ConnectionState.Updating)) {
            val connection = CountingConnection(state)
            ResidentConnection.resume(connection)
            assertEquals("$state", 0, connection.reconnects)
        }
    }

    @Test fun aConnectionThatIsNotUpIsKicked() {
        for (state in listOf(ConnectionState.Connecting, ConnectionState.WaitingForNetwork,
            ConnectionState.NeedsLocalNetworkPermission, ConnectionState.CertificateMismatch, ConnectionState.Revoked)) {
            val connection = CountingConnection(state)
            ResidentConnection.resume(connection)
            assertEquals("$state", 1, connection.reconnects)
        }
    }

    // With two hub groups, resume read only the selected group's state and reconnect() restarted both: a selected
    // group offline dropped the other's live connection, and a selected group online left the other waiting (#5311).
    @Test fun eachGroupIsResumedOnItsOwnState() {
        for ((selected, other) in listOf(ConnectionState.Connecting to ConnectionState.Online,
            ConnectionState.Online to ConnectionState.Connecting)) {
            val connection = TwoGroupConnection(selected, other)
            ResidentConnection.resume(connection)
            assertEquals("$selected/$other selected", if (selected == ConnectionState.Online) 0 else 1, connection.selectedReconnects)
            assertEquals("$selected/$other other", if (other == ConnectionState.Online) 0 else 1, connection.otherReconnects)
        }
    }

    @Test fun theWatchdogWakesEveryGroupWithoutDroppingLiveConnections() {
        val connection = TwoGroupConnection(ConnectionState.Online, ConnectionState.Updating)
        ResidentConnection.wake(connection)
        assertEquals(1, connection.selectedWakes)
        assertEquals(1, connection.otherWakes)
        assertEquals(0, connection.selectedReconnects)
        assertEquals(0, connection.otherReconnects)
    }

    // The service registered its own default network callback that called reconnect() on every onAvailable / onLost,
    // on top of the runtime's, which reconnects only when the network really changed: each change dropped the
    // connection twice. The service must not watch the network itself.
    @Test fun theServiceDoesNotWatchTheNetwork() {
        val source = java.io.File(System.getProperty("persona.repo.root"),
            "android/platform/notify/src/main/kotlin/persona/platform/notify/HubService.kt").readText()
        assertEquals(false, source.contains("NetworkCallback"))
    }
}
