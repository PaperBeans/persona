package persona.core.hub

import persona.core.contracts.CoreText.Key
import persona.core.contracts.CoreText
import persona.core.contracts.HubScope
import persona.core.contracts.JsonCodec
import persona.core.contracts.SessionKey
import persona.core.contracts.api.*
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.withContext
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.serialization.json.*
import okhttp3.HttpUrl
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.Request
import okhttp3.RequestBody
import okhttp3.RequestBody.Companion.asRequestBody
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import java.io.File
import java.util.UUID
import java.util.concurrent.TimeUnit

/** Decorates the existing transport's readiness gate without changing the outbox payload format. */
class HttpChatComposerService internal constructor(
    private val hubScope: HubScope,
    private val transport: MessageTransport,
    private val request: suspend (String, RequestBody, Map<String, String>) -> Response,
    /** A refusal retry's delivery, with the operation id the retry goes under. */
    private val onRetryDelivery: suspend (String, ComposerDelivery, String) -> Unit = { _, _, _ -> },
    private val onPairingExpired: () -> Unit = {},
) : ChatComposerService, MessageTransport by transport {
    constructor(
        hubScope: HubScope, transport: MessageTransport, connection: OkHttpHubConnection, token: String,
        onRetryDelivery: suspend (String, ComposerDelivery, String) -> Unit,
        onPairingExpired: () -> Unit,
        isCurrent: () -> Boolean = { true },
    ) :
        this(hubScope, transport, { path, body, headers ->
            if (!isCurrent()) throw CancellationException("Pairing changed")
            val route = connection.route.value ?: throw HubFailure(HubError.NoNetwork)
            val url = route.endpoint.baseUrl().hubUrl(path)
            // A file goes up in one request that the hub answers once the computer has all of it: on a slow link that
            // takes minutes (the old app allowed 10), not the 15 s every other call gets.
            val client = if (path.contains("/files?")) connection.http.client.newBuilder()
                .writeTimeout(10, TimeUnit.MINUTES).readTimeout(10, TimeUnit.MINUTES).build() else connection.http.client
            client.newCall(Request.Builder().url(url).header("Authorization", "Bearer $token")
                .apply { headers.forEach { (name, value) -> header(name, value) } }.post(body).build()).awaitResponse().also {
                    if (!isCurrent()) {
                        it.close()
                        throw CancellationException("Pairing changed")
                    }
                    if (connection.route.value !== route) {
                        it.close()
                        throw HubFailure(HubError.Transport, "Route changed before receipt")
                    }
                }
        }, onRetryDelivery, onPairingExpired)

    override val deliveries = MutableStateFlow<Map<String, ComposerDelivery>>(emptyMap())
    private val refused = mutableMapOf<String, MessageRequest>()
    private val sending = Mutex()

    override suspend fun send(request: MessageRequest): MessageTransportResult = sending.withLock {
        sendLocked(request)
    }

    private suspend fun sendLocked(message: MessageRequest): MessageTransportResult {
        require(message.scope == hubScope)
        if (!readyToSend.value) return MessageTransportResult.NotSent
        val body = buildJsonObject {
            put("text", persona.core.contracts.ProtocolCompatibility.messageForHub(message.text))
            put("client_msg_id", message.opId)
            message.clearDraft?.let { put("clear_draft", it) }
            message.launcher?.let { put("launcher", it) }
            message.model?.let { put("model", it) }
            if (message.applySwitch) put("apply_switch", true)
        }.toString().toRequestBody(jsonType)
        return try {
            request(api("sessions", message.session.machineId, message.session.sessionId, "messages"), body, message.headers).use { response ->
                if (response.isSuccessful) {
                    val fields = runCatching { JsonCodec.parseToJsonElement(response.peekBody(64 * 1024).string()).jsonObject }.getOrNull()
                    if (fields?.get("state")?.jsonPrimitive?.contentOrNull in setOf("pending", "unknown")) {
                        record(message, ComposerDelivery(DeliveryState.Unknown))
                        return@use MessageTransportResult.NoReceipt
                    }
                    refused.remove(message.clientMessageId)
                    record(message, ComposerDelivery(DeliveryState.Sent))
                    MessageTransportResult.Accepted
                } else {
                    val failure = HubFailure.from(response)
                    if (failure.kind == HubError.PairingExpired) onPairingExpired()
                    val fields = runCatching { JsonCodec.parseToJsonElement(response.peekBody(64 * 1024).string()).jsonObject }.getOrNull()
                    val refusal = if (failure.delivered == "none" && failure.code in setOf("input_draft", "dialog_open")) {
                        refused[message.clientMessageId] = message
                        ComposerRefusal(message.session, message.clientMessageId, failure.code!!,
                            fields?.get("draft")?.jsonPrimitive?.contentOrNull,
                            fields?.get("clearable")?.jsonPrimitive?.contentOrNull)
                    } else null
                    val state = when {
                        failure.delivered == "yes" -> DeliveryState.Sent
                        failure.delivered == "none" -> DeliveryState.Refused
                        else -> DeliveryState.Unknown
                    }
                    record(message, ComposerDelivery(state, refusal))
                    MessageTransportResult.HubError(failure.code, failure.delivered, failure.message, failure.retryable)
                }
            }
        } catch (cancelled: CancellationException) { throw cancelled }
        catch (error: Exception) {
            val kind = HubFailure.from(error).kind
            if (kind in setOf(HubError.NoNetwork, HubError.AddressNotFound, HubError.ConnectionRefused, HubError.Unreachable)) {
                MessageTransportResult.NotSent
            } else {
                record(message, ComposerDelivery(DeliveryState.Unknown))
                MessageTransportResult.NoReceipt
            }
        }
    }

    override suspend fun retryRefused(clientMessageId: String, clearDraft: Boolean): ComposerDelivery = sending.withLock {
        val original = refused[clientMessageId] ?: throw ComposerOperationException(CoreText.of(Key.ComposerCheckTerminal))
        val refusal = deliveries.value[clientMessageId]?.refusal
            ?: throw ComposerOperationException(CoreText.of(Key.ComposerUnconfirmed))
        val clearable = if (clearDraft) refusal.clearable?.takeIf { it.isNotEmpty() }
            ?: throw ComposerOperationException(CoreText.of(Key.ComposerDraftInTerminal)) else null
        if (!readyToSend.value) throw ComposerOperationException(CoreText.of(Key.ComposerOffline))
        // The refused id is spent: the hub answers it again from its first try (the 409, or op_id_reused once
        // clear_draft is added), so the retry goes under a new one, as on iOS.
        val retry = original.copy(clearDraft = clearable, opId = UUID.randomUUID().toString())
        onRetryDelivery(clientMessageId, ComposerDelivery(DeliveryState.Sending), retry.opId)
        record(original, ComposerDelivery(DeliveryState.Sending))
        val result = try { sendLocked(retry) }
        catch (cancelled: CancellationException) {
            val unknown = ComposerDelivery(DeliveryState.Unknown)
            record(original, unknown)
            withContext(NonCancellable) { onRetryDelivery(clientMessageId, unknown, retry.opId) }
            throw cancelled
        }
        if (result == MessageTransportResult.NotSent) record(original, ComposerDelivery(DeliveryState.Refused, refusal))
        deliveries.value.getValue(clientMessageId).also { onRetryDelivery(clientMessageId, it, retry.opId) }
    }

    override suspend fun upload(session: SessionKey, name: String, file: File): String {
        if (!readyToSend.value) throw ComposerOperationException(CoreText.of(Key.ComposerUploadOffline))
        if (file.length() > 200L * 1024 * 1024) throw ComposerOperationException(CoreText.of(Key.ComposerFileTooLarge, 200))
        val url = path("machines", session.machineId, "files").addQueryParameter("name", name).build()
        return request(url.encodedPath + "?" + url.encodedQuery,
            file.asRequestBody("application/octet-stream".toMediaType()), emptyMap()).use { response ->
            if (!response.isSuccessful) {
                val failure = HubFailure.from(response)
                if (failure.kind == HubError.PairingExpired) onPairingExpired()
                throw ComposerOperationException(CoreText.of(when {
                    response.code == 404 -> Key.UploadHubUnsupported
                    failure.code == "node_outdated" -> Key.UploadNodeOutdated
                    response.code == 413 -> Key.UploadTooLarge
                    else -> Key.UploadFailed
                }))
            }
            JsonCodec.parseToJsonElement(response.body!!.string()).jsonObject["path"]!!.jsonPrimitive.content.also {
                require(it.startsWith('/') && !it.contains('\n') && !it.contains('\r'))
            }
        }
    }

    override suspend fun interrupt(session: SessionKey) {
        if (!readyToSend.value) throw ComposerOperationException(CoreText.of(Key.ComposerInterruptOffline))
        request(api("sessions", session.machineId, session.sessionId, "interrupt"), "{}".toRequestBody(jsonType), emptyMap()).use {
            if (!it.isSuccessful) {
                if (HubFailure.from(it).kind == HubError.PairingExpired) onPairingExpired()
                throw ComposerOperationException(CoreText.of(Key.ComposerInterruptUnconfirmed))
            }
        }
    }

    private fun record(request: MessageRequest, delivery: ComposerDelivery) {
        deliveries.update { it + (request.clientMessageId to delivery) }
    }

    private fun path(vararg segments: String) = HttpUrl.Builder().scheme("https").host("example.com")
        .addPathSegments("api/v1").apply { segments.forEach(::addPathSegment) }
    private fun api(vararg segments: String) = path(*segments).build().encodedPath
    private val jsonType = "application/json; charset=utf-8".toMediaType()
}
