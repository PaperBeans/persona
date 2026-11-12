package persona.core.contracts.api

import persona.core.contracts.SessionKey
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import java.time.Instant

/** Ephemeral local sending and hub receipts are separate from the durable outbox and runtime state. */
data class MessageSendReceipt(
    val session: SessionKey,
    val clientMessageId: String,
    val state: String,
    val at: Instant,
    val mainTarget: Boolean = false,
) {
    val processing: Boolean get() = state == "processing" || state == "delivered"
}

private val noSendReceipts = MutableStateFlow<List<MessageSendReceipt>>(emptyList()).asStateFlow()

interface MessageSendReceiptSource {
    val sendReceipts: StateFlow<List<MessageSendReceipt>> get() = noSendReceipts
}
