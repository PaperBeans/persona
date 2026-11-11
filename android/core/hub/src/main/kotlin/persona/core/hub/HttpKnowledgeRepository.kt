package persona.core.hub

import persona.core.contracts.HubScope
import persona.core.contracts.JsonCodec
import persona.core.contracts.api.*
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.SerializationException
import kotlinx.serialization.decodeFromString
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.OkHttpClient
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody

class HttpKnowledgeRepository(
    override val scope: HubScope,
    baseUrl: String,
    private val token: String,
    client: OkHttpClient,
    private val isCurrent: () -> Boolean,
    private val dispatcher: CoroutineDispatcher = Dispatchers.Default,
) : KnowledgeRepository {
    private val base = baseUrl.toHttpUrl().also {
        // A path prefix is a hub behind a reverse proxy at a sub-path; the segments below are added under it.
        require(it.username.isEmpty() && it.password.isEmpty() && it.query == null && it.fragment == null)
    }
    private val http = client.newBuilder().followRedirects(false).followSslRedirects(false)
        .retryOnConnectionFailure(false).build()

    private fun url(vararg parts: String): HttpUrl = base.newBuilder()
        .addPathSegment("api").addPathSegment("v1").apply { parts.forEach(::addPathSegment) }.build()

    private fun checkScope() {
        if (!isCurrent()) throw CancellationException("Pairing changed")
    }

    // The knowledge base is the current assistant's (the hub client names it), unless the calling page pinned its own
    // (an entry's page keeps the assistant it was opened for, docs/assistant/managers.md "Clients"): put on the URL here.
    private suspend inline fun <reified T> get(url: HttpUrl): T {
        checkScope()
        val request = Request.Builder().url(url.withRequestManager()).header("Authorization", "Bearer $token").build()
        val body = http.newCall(request).awaitResponse().use { response ->
            checkScope()
            if (!response.isSuccessful) throw HubFailure.from(response)
            response.body?.string() ?: throw HubFailure(HubError.Protocol)
        }
        val decoded = withContext(dispatcher) {
            try { JsonCodec.decodeFromString<T>(body) }
            catch (error: SerializationException) { throw HubFailure(HubError.Protocol, cause = error) }
        }
        checkScope()
        return decoded
    }

    override suspend fun list(kind: KnowledgeKind, tag: String?): KnowledgeList = get(
        url("kb", kind.wire).newBuilder().apply {
            tag?.takeIf { it.isNotBlank() }?.let { addQueryParameter("tag", it) }
        }.build(),
    )

    override suspend fun detail(kind: KnowledgeKind, id: String): KnowledgeDetail = entry(kind, id).detail

    override suspend fun entry(kind: KnowledgeKind, id: String): KnowledgeDetailResponse {
        require(id.isNotBlank() && id != "." && id != "..")
        val response = get<KnowledgeDetailResponse>(url("kb", kind.wire, id))
        if (response.detail.kind != kind.wire || response.detail.id != id) throw HubFailure(HubError.Protocol)
        // The hub lists live, non-assistant sessions; keep that promise even if an older hub sends more.
        return response.copy(agents = response.agents?.filter { !it.assistant && it.isLive })
    }

    override suspend fun update(kind: KnowledgeKind, id: String, update: KnowledgeUpdate) {
        require(id.isNotBlank() && id != "." && id != "..")
        checkScope()
        val body = JsonCodec.encodeToString(KnowledgeUpdate.serializer(), update)
        val request = Request.Builder().url(url("kb", kind.wire, id).withRequestManager()).header("Authorization", "Bearer $token")
            .put(body.toRequestBody("application/json".toMediaType())).build()
        http.newCall(request).awaitResponse().use { response ->
            checkScope()
            if (!response.isSuccessful) throw HubFailure.from(response)
        }
    }

    override suspend fun makeMinutes(id: String) = post(url("recordings", recording(id), "minutes"), null)

    override suspend fun addMinutesTodos(id: String, version: Int, actions: List<String>) {
        require(actions.isNotEmpty())
        post(url("recordings", recording(id), "minutes", "todos"), buildJsonObject {
            put("version", JsonPrimitive(version))
            put("actions", JsonArray(actions.map(::JsonPrimitive)))
        })
    }

    private fun recording(id: String) = id.also { require(it.isNotBlank() && it != "." && it != "..") }

    /** A write to the knowledge base's recordings: its answer isn't needed, the page reads the recording back. */
    private suspend fun post(url: HttpUrl, body: JsonObject?) {
        checkScope()
        val request = Request.Builder().url(url.withRequestManager()).header("Authorization", "Bearer $token")
            .post((body?.toString() ?: "{}").toRequestBody("application/json".toMediaType())).build()
        http.newCall(request).awaitResponse().use { response ->
            checkScope()
            if (!response.isSuccessful) throw HubFailure.from(response)
        }
    }

    override suspend fun search(query: String): List<KnowledgeHit> {
        if (query.isBlank()) return emptyList()
        return get<KnowledgeSearch>(url("kb-search").newBuilder()
            .addQueryParameter("q", query.trim()).build()).hits
    }
}
