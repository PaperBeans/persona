package persona.core.contracts.api

import persona.core.contracts.SessionKey
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow

enum class DeliveryState {
    Sending, WaitingForNetwork, Sent, Rejected, NeedsConfirmation,
    Queued, Unknown, Refused, Verifying,
}

data class OutboxMessage(
    val clientMessageId: String,
    val session: SessionKey,
    val text: String,
    val state: DeliveryState,
    val error: String? = null,
    val mainTarget: Boolean = false,
    /** When this phone wrote it, epoch millis. Absent on an outbox restored from before that was stored. */
    val createdAt: Long? = null,
)

private val NoWrittenAt = MutableStateFlow(emptyMap<String, Long>())

interface MessageSender : MessageSendReceiptSource {
    val outbox: StateFlow<List<OutboxMessage>>
    /** When this phone wrote each send, including ones already in the transcript. Epoch millis, by client message id. */
    val writtenAt: StateFlow<Map<String, Long>> get() = NoWrittenAt
    suspend fun send(session: SessionKey, text: String, clientMessageId: String, clearDraft: String? = null)
    /** [launcher] and [model] apply to this message; null keeps the hub's configured default. */
    suspend fun send(
        session: SessionKey, text: String, clientMessageId: String, clearDraft: String?,
        launcher: String?, model: String?,
    ) {
        require(launcher == null && model == null) { "Launcher/model overrides are not supported" }
        send(session, text, clientMessageId, clearDraft)
    }
    suspend fun cancel(clientMessageId: String): Boolean = false
    /**
     * Undo of a [cancel] (#4645): [message], as the outbox listed it before, back in its state with its refusal, so it
     * shows (and a queued or waiting one goes out) again. Nothing once it is no longer cancelled.
     */
    fun restore(message: OutboxMessage) = Unit
    /**
     * Sends a refused message, or one whose delivery is to be confirmed, again; one still on its way (queued, waiting for
     * the network) is tried at once.
     */
    suspend fun retry(clientMessageId: String): Boolean = false
    /** Drops a message whose delivery is unknown: off the list and the queue, never retried or reconciled. */
    suspend fun discard(clientMessageId: String): Boolean = false
    suspend fun confirm(clientMessageId: String, delivered: Boolean): Boolean = false
}
