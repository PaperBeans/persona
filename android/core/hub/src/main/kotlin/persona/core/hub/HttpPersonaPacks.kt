package persona.core.hub

import persona.core.contracts.ProtocolCompatibility

import okhttp3.HttpUrl
import okhttp3.Response
import persona.core.contracts.JsonCodec
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.RequestBody.Companion.toRequestBody
import persona.core.contracts.api.LookCurrent
import persona.core.contracts.api.LookListing
import persona.core.contracts.api.LookDeleted
import persona.core.contracts.api.LookUploaded
import java.util.Base64
import persona.core.contracts.api.PersonaInfo
import persona.core.contracts.api.PersonaListing
import persona.core.contracts.api.PersonaPacks
import persona.core.contracts.api.PersonaPacksUnsupported

/**
 * GET /api/v1/personas and its files, and the looks (GET /api/v1/looks, PUT …/current, POST …/uploads, DELETE …/uploads/{id}),
 * on the active pairing's authenticated route.
 */
class HttpPersonaPacks internal constructor(
    /** PUT with a JSON body (the choice). */
    private val send: suspend (String, String) -> Response = { _, _ -> throw HubFailure(HubError.Server, "HTTP 404", status = 404) },
    /** POST with a JSON body or DELETE (method, path, body): uploads. */
    private val call: suspend (String, String, String?) -> Response = { _, _, _ -> throw HubFailure(HubError.Server, "HTTP 404", status = 404) },
    /** The hub's features from its `hello`. */
    private val features: () -> Collection<String> = { emptyList() },
    private val request: suspend (String) -> Response,
) : PersonaPacks {
    constructor(connection: OkHttpHubConnection) : this(
        { path, json -> connection.request(path, "PUT", json.toRequestBody("application/json".toMediaType())) },
        { method, path, json ->
            // The hub draws the avatar's sizes and state frames before it answers: more than the client's usual 15 s on a slow machine.
            connection.request(path, method, json?.toRequestBody("application/json".toMediaType()), readTimeoutSeconds = UPLOAD_TIMEOUT_SECONDS)
        },
        { connection.lastHello?.features.orEmpty() },
        { path -> connection.request(path, "GET", null) },
    )

    override val uploads: Boolean get() = "look_uploads" in features()

    override suspend fun upload(name: String, subtitle: String?, image: ByteArray, choose: Boolean): LookUploaded {
        val body = buildJsonObject {
            put("name", name)
            subtitle?.takeIf { it.isNotBlank() }?.let { put("subtitle", it) }
            put("image", Base64.getEncoder().encodeToString(image))
            if (choose) put("choose", true)
        }.toString()
        return call("POST", "/api/v1/looks/uploads", body).use {
            if (!it.isSuccessful) throw HubFailure.from(it)
            JsonCodec.decodeFromString<LookUploaded>(it.body?.string().orEmpty()).let { made ->
                made.copy(current = made.current?.let(::compatibleLookCurrent))
            }
        }
    }

    override suspend fun deleteUpload(id: String): LookCurrent {
        val path = HttpUrl.Builder().scheme("https").host("example.com").addPathSegments("api/v1/looks/uploads").addPathSegment(id).build().encodedPath
        return call("DELETE", path, null).use {
            if (!it.isSuccessful) throw HubFailure.from(it)
            compatibleLookCurrent(JsonCodec.decodeFromString<LookDeleted>(it.body?.string().orEmpty()).current)
        }
    }

    override suspend fun looks(): LookListing = try {
        request("/api/v1/looks").use {
            if (!it.isSuccessful) throw HubFailure.from(it)
            JsonCodec.decodeFromString<LookListing>(it.body?.string().orEmpty()).let { listing ->
                listing.copy(looks = listing.looks.map { look ->
                    if (look.source == "builtin") look.copy(id = ProtocolCompatibility.canonicalBuiltin(look.id)!!, appIcon = ProtocolCompatibility.canonicalBuiltin(look.appIcon)!!) else look
                }, current = compatibleLookCurrent(listing.current))
            }
        }
    } catch (failure: HubFailure) {
        throw if (failure.status == 404) PersonaPacksUnsupported() else failure
    }

    override suspend fun choose(id: String, ifUnset: Boolean): LookCurrent = try {
        val body = buildJsonObject { put("id", ProtocolCompatibility.builtinForHub(id)); if (ifUnset) put("if_unset", true) }.toString()
        send("/api/v1/looks/current", body).use {
            if (!it.isSuccessful) throw HubFailure.from(it)
            compatibleLookCurrent(JsonCodec.decodeFromString<LookCurrent>(it.body?.string().orEmpty()))
        }
    } catch (failure: HubFailure) {
        throw if (failure.status == 404) PersonaPacksUnsupported() else failure
    }

    override suspend fun list(): List<PersonaInfo> = try {
        request("/api/v1/personas").use {
            if (!it.isSuccessful) throw HubFailure.from(it)
            JsonCodec.decodeFromString<PersonaListing>(it.body?.string().orEmpty()).personas
        }
    } catch (failure: HubFailure) {
        // The connection throws for any non-2xx answer; an older hub has no such route.
        throw if (failure.status == 404) PersonaPacksUnsupported() else failure
    }

    override suspend fun file(id: String, file: String): ByteArray {
        val path = HttpUrl.Builder().scheme("https").host("example.com").addPathSegments("api/v1/personas")
            .addPathSegment(id).addPathSegments(file).build().encodedPath
        return request(path).use {
            if (!it.isSuccessful) throw HubFailure.from(it)
            it.body?.bytes() ?: ByteArray(0)
        }
    }

    private companion object {
        const val UPLOAD_TIMEOUT_SECONDS = 60L
    }
}
