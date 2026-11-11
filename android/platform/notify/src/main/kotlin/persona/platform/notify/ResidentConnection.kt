package persona.platform.notify

import persona.core.contracts.api.ConnectionState
import persona.core.contracts.api.HubConnection

/** What the resident service does to the connection when it starts (#4139). */
internal object ResidentConnection {
    /**
     * Kicks a connection that is not up (cutting a retry wait short); leaves a live one alone, so starting the
     * service (boot, the app coming to the front) does not drop it and flash "Connecting". Each hub group is judged
     * on its own state, so one group being down neither drops nor hides another (#5311).
     */
    fun resume(connection: HubConnection) {
        connection.groupLinks.forEach { group ->
            when (group.state.value) {
                ConnectionState.Online, ConnectionState.Updating -> Unit
                else -> group.reconnect()
            }
        }
    }

    /** Asks every group to validate its socket now, even when the selected group already looks online. */
    fun wake(connection: HubConnection) {
        connection.groupLinks.forEach { it.wake() }
    }
}
