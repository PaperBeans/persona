package persona.core.hub

import persona.core.contracts.JsonCodec
import persona.core.contracts.Machine
import persona.core.contracts.api.ComputerCode
import persona.core.contracts.api.ComputerOs
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.put
import kotlinx.serialization.json.putJsonObject
import okhttp3.HttpUrl
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.RequestBody
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response

/**
 * Machines › "Add computer" on one hub, in its own machine ids (docs/protocol/protocol.md "Computer pairing codes" "Computer trust"): a computer's code
 * (`POST /api/v1/auth/pair-codes` with `add` or `machine`), the machine list, and confirm / reject.
 */
class HttpComputerPairing internal constructor(
    private val request: suspend (String, String, RequestBody?) -> Response,
) {
    constructor(connection: OkHttpHubConnection) : this({ path, method, body -> connection.request(path, method, body) })

    /** [manager]: the manager the computer is for (`add.manager`, its id as the roster has it); null names none. */
    suspend fun add(name: String, os: ComputerOs, manager: String? = null): ComputerCode = code(buildJsonObject {
        putJsonObject("add") {
            put("name", name.trim()); put("os", os.wire)
            manager?.takeIf { it.isNotBlank() }?.let { put("manager", it) }
        }
    })

    suspend fun again(machineId: String): ComputerCode = code(buildJsonObject { put("machine", machineId) })

    suspend fun machines(): List<Machine> {
        val list = JsonCodec.parseToJsonElement(call("/api/v1/machines", "GET")).let { (it as? JsonObject)?.get("machines") as? JsonArray }
        return list?.let { JsonCodec.decodeFromJsonElement(ListSerializer(Machine.serializer()), it) }.orEmpty()
    }

    suspend fun confirm(machineId: String) { call(path(machineId, "confirm"), "POST", "{}".toRequestBody(json)) }

    suspend fun reject(machineId: String) { call(path(machineId, "reject"), "POST", "{}".toRequestBody(json)) }

    private suspend fun code(body: JsonObject): ComputerCode {
        val data = JsonCodec.parseToJsonElement(call("/api/v1/auth/pair-codes", "POST", body.toString().toRequestBody(json))) as? JsonObject
            ?: throw HubFailure(HubError.Protocol)
        val machine = data.text("machine") ?: throw HubFailure(HubError.Protocol)
        val pairUrl = data.text("pair_url") ?: throw HubFailure(HubError.Protocol)
        return ComputerCode(machine, data.text("name") ?: machine, pairUrl, HttpDeviceSettings.wireMillis(data.text("expires_at")),
            trust = data.text("trust") ?: "pending", added = (data["added"] as? JsonPrimitive)?.booleanOrNull == true,
            command = data.text("command"), linuxInstall = (data["install"] as? JsonObject)?.text("linux"), manager = data.text("manager"))
    }

    private fun path(machineId: String, action: String): String {
        require(machineId.isNotBlank())
        return HttpUrl.Builder().scheme("https").host("example.com").addPathSegments("api/v1/machines").addPathSegment(machineId)
            .addPathSegment(action).build().encodedPath
    }

    private fun JsonObject.text(key: String): String? = (this[key] as? JsonPrimitive)?.contentOrNull?.takeIf { it.isNotBlank() }

    private suspend fun call(path: String, method: String, body: RequestBody? = null): String =
        request(path, method, body).use {
            if (!it.isSuccessful) throw HubFailure.from(it)
            it.body?.string().orEmpty()
        }

    private companion object {
        val json = "application/json; charset=utf-8".toMediaType()
    }
}

