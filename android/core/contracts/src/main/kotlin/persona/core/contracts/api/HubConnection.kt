package persona.core.contracts.api

import persona.core.contracts.Hello
import persona.core.contracts.ServerEvent
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.serialization.json.JsonObject

enum class ConnectionState {
    Online, Connecting, Updating, WaitingForNetwork, NeedsLocalNetworkPermission, CertificateMismatch, Revoked,
}

interface HubConnection {
    val state: StateFlow<ConnectionState>

    /** Everything the hub pushes over the WebSocket, decoded. Hot: a late collector misses earlier events. */
    val events: SharedFlow<ServerEvent>

    /**
     * Whether the socket now open resumed the hub's broadcast stream (#1574): the hub replayed everything this client
     * missed while it was away, so what reacts to coming back Online need not refetch. False before a hello, and for
     * hubs that do not resume.
     */
    val resumed: Boolean get() = false

    /**
     * The hello the selected hub sent when this connection last opened (its build, protocol, the app build it needs),
     * kept for pages built after it went by on [events]. Null before any, and for connections that do not keep it.
     */
    val lastHello: Hello? get() = null

    fun reconnect()

    /**
     * Each open hub group's own state and recovery actions, for callers that must act per group (#5311): [reconnect]
     * restarts every group while [state] is only the selected one's. A single connection is its own only group.
     */
    val groupLinks: List<GroupLink> get() = listOf(GroupLink(state, ::reconnect))

    /** Sends one client message (subscribe, seen, register_phone…) over the open WebSocket; false when not connected. */
    fun send(message: JsonObject): Boolean
}

/**
 * One hub group as [HubConnection.groupLinks] shows it. [wake] validates an open socket with its bounded ping/pong
 * recovery; the two-argument constructor keeps a basic connection's wake equivalent to reconnecting it.
 */
class GroupLink(val state: StateFlow<ConnectionState>, val reconnect: () -> Unit, val wake: () -> Unit) {
    constructor(state: StateFlow<ConnectionState>, reconnect: () -> Unit) : this(state, reconnect, reconnect)
}
