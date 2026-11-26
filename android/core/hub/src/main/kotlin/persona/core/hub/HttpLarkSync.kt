package persona.core.hub

import kotlinx.serialization.KSerializer
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import okhttp3.HttpUrl
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.RequestBody
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import persona.core.contracts.JsonCodec
import persona.core.contracts.api.BindPrompt
import persona.core.contracts.api.LarkSyncAuthorize
import persona.core.contracts.api.LarkSyncCreated
import persona.core.contracts.api.LarkSyncSettings
import persona.core.contracts.api.LarkSyncView
import java.net.URLEncoder

/**
 * Settings › Lark bot › "Daily sync to your knowledge base" over the pairing's connection (web `lib/lark-sync.ts`, hub
 * `extmgr/larksync_routes.go`). A hub without the sync answers the view with 404 / 405 / 501, or with `available`
 * false: both are null, and the page draws nothing of it. Registration steps ask Feishu's open platform first, so they
 * wait longer than the client's usual read timeout.
 */
class HttpLarkSync internal constructor(
    private val request: suspend (path: String, method: String, body: RequestBody?, readTimeoutSeconds: Long?) -> Response,
    /** The hub's web address the app reaches it at now; null while it has none. */
    private val base: () -> HttpUrl?,
) : LarkSyncSettings {
    constructor(connection: OkHttpHubConnection) : this({ path, method, body, timeout ->
        connection.request(path, method, body, readTimeoutSeconds = timeout)
    }, { connection.route.value?.endpoint?.let { runCatching { it.baseUrl() }.getOrNull() } })

    override suspend fun view(): LarkSyncView? = try { viewOf(call(Sync, "GET")) } catch (failure: HubFailure) {
        if (failure.status in Absent) null else throw failure
    }

    override suspend fun setSchedule(hour: Int) = viewOf(call(Sync, "PUT", buildJsonObject { put("schedule_hour", hour) }))

    override suspend fun createBot(label: String, tier: String, region: String) = decode(LarkSyncCreated.serializer(),
        call("$Sync/bots", "POST", buildJsonObject { put("label", label); put("tier", tier); put("region", region) }, Slow))

    override suspend fun register(botId: String, step: String) =
        decode(BindPrompt.serializer(), call("${bot(botId)}/register", "POST", buildJsonObject { put("step", step) }, Slow))

    override suspend fun addScopes(botId: String, tier: String) =
        decode(BindPrompt.serializer(), call("${bot(botId)}/scopes", "POST", buildJsonObject { put("tier", tier) }, Slow))

    override suspend fun removeBot(botId: String) = viewOf(call(bot(botId), "DELETE"))

    override suspend fun authorize(botId: String, redirectUri: String, tier: String?) = decode(LarkSyncAuthorize.serializer(),
        call("${bot(botId)}/authorize", "POST", buildJsonObject {
            put("redirect_uri", redirectUri)
            tier?.takeIf { it.isNotEmpty() }?.let { put("tier", it) }
        }, Slow))

    override suspend fun pollAuthorize(botId: String) = decode(BindPrompt.serializer(), call("${bot(botId)}/authorize/poll", "POST", JsonObject(emptyMap())))

    override suspend fun finishOAuth(state: String, code: String) =
        viewOf(call("$Sync/oauth", "POST", buildJsonObject { put("state", state); put("code", code) }, Slow))

    override suspend fun setBindingEnabled(key: String, enabled: Boolean) =
        viewOf(call(binding(key), "PUT", buildJsonObject { put("enabled", enabled) }))

    override suspend fun removeBinding(key: String) = viewOf(call(binding(key), "DELETE"))

    override suspend fun runBinding(key: String) = viewOf(call("${binding(key)}/run", "POST", JsonObject(emptyMap())))

    override suspend fun oauthRedirectUri(): String? = base()?.let(::oauthRedirectUri)

    // The pairing's connection throws a HubFailure for a status that is not 2xx; a bare request returns it.
    private suspend fun call(path: String, method: String, body: JsonElement? = null, timeout: Long? = null): String =
        request(path, method, body?.toString()?.toRequestBody(json), timeout).use { response ->
            if (!response.isSuccessful) throw HubFailure.from(response)
            response.body?.string().orEmpty()
        }

    private fun <T> decode(serializer: KSerializer<T>, body: String): T = try { JsonCodec.decodeFromString(serializer, body) }
    catch (error: SerializationException) { throw HubFailure(HubError.Protocol, cause = error) }
    catch (error: IllegalArgumentException) { throw HubFailure(HubError.Protocol, cause = error) }

    companion object {
        private const val Sync = "/api/v1/bots/feishu/sync"
        /** An older hub has no such route. */
        private val Absent = setOf(404, 405, 501)
        private val json = "application/json; charset=utf-8".toMediaType()
        /** The hub asks the open platform before it answers a registration, an authorization or a code. */
        private const val Slow = 40L
        /** The page the open platform sends the owner back to; the hub accepts only this path. */
        const val OAuthReturnPath = "/lark-oauth"

        private fun segment(value: String) = URLEncoder.encode(value, "UTF-8").replace("+", "%20")
        private fun bot(id: String) = "$Sync/bots/${segment(id)}"
        private fun binding(key: String) = "$Sync/bindings/${segment(key)}"

        /** [base] (with the path prefix a hub behind a proxy is served under) and `/lark-oauth`. */
        fun oauthRedirectUri(base: HttpUrl): String = base.hubUrl(OAuthReturnPath).toString()

        /**
         * A view the page can draw, or null: no Lark module (`available` false), or another route's answer (an older
         * hub). An hour out of range is the hub's default, 3.
         */
        internal fun viewOf(body: String): LarkSyncView? {
            val raw = try { JsonCodec.parseToJsonElement(body) } catch (_: SerializationException) { return null }
            val available = ((raw as? JsonObject)?.get("available") as? JsonPrimitive)?.content == "true"
            if (!available || raw !is JsonObject || raw["bots"] !is kotlinx.serialization.json.JsonArray ||
                raw["bindings"] !is kotlinx.serialization.json.JsonArray) return null
            val view = try { JsonCodec.decodeFromJsonElement(LarkSyncView.serializer(), raw) }
            catch (error: SerializationException) { throw HubFailure(HubError.Protocol, cause = error) }
            catch (error: IllegalArgumentException) { throw HubFailure(HubError.Protocol, cause = error) }
            return if (view.scheduleHour in 0..23) view else view.copy(scheduleHour = 3)
        }
    }
}
