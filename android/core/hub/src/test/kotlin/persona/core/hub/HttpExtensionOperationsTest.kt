package persona.core.hub

import persona.core.contracts.ExtensionOperationEvent
import persona.core.contracts.ServerEvent
import persona.core.contracts.api.ExtensionPreview
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.*
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.mockwebserver.MockResponse
import persona.core.hub.ProtocolCompatibilityMockServer as MockWebServer
import org.junit.Assert.*
import org.junit.Test
import persona.core.contracts.api.ExtensionPartialUninstall
import persona.core.contracts.api.ExtensionFailedMember

/** Installing in steps (docs/protocol/protocol.md "Extension install, upgrade and uninstall progress", #3277): the routes, their bodies and the pushed event. */
class HttpExtensionOperationsTest {
    private fun <T> withHub(block: suspend (MockWebServer, HttpExtensionManagement) -> T): T = runBlocking {
        MockWebServer().use { server ->
            val http = OkHttpClient.Builder().retryOnConnectionFailure(false).build()
            block(server, HttpExtensionManagement({ path, method, body ->
                http.newCall(Request.Builder().url(server.url(path)).method(method, body).build()).execute()
            }))
        }
    }

    private val op = """{"id":"0a1b2c","kind":"install","extension":"example-notes","market":"m1","state":"running","step":"download",
        "steps":[{"id":"download","state":"running","bytes":1048576,"total":16777216},{"id":"verify","state":"pending"},
        {"id":"teleport","state":"warming","future":true}],"retryable":false,"started_at":"2026-11-06T08:00:00Z",
        "updated_at":"2026-11-06T08:00:01.5Z","added_later":{"x":1}}"""

    private fun body(server: MockWebServer) = server.takeRequest().let { it.method + " " + it.path + " " + it.body.readUtf8() }

    @Test fun theRoutesAndWhatTheySend() = withHub { server, api ->
        server.enqueue(MockResponse().setResponseCode(202).setBody("""{"operation":$op}"""))
        val started = api.startFromMarket("m1", "example-notes")
        val first = server.takeRequest()
        assertEquals("POST /api/v1/extensions/-/catalog/preview", "${first.method} ${first.path}")
        assertEquals(buildJsonObject { put("market", "m1"); put("extension", "example-notes"); put("async", true) },
            Json.parseToJsonElement(first.body.readUtf8()))
        assertEquals("0a1b2c", started.id)
        assertEquals(16777216L, started.steps[0].total)
        // A step a newer hub adds, in a state the app does not know, is kept as it is.
        assertEquals("teleport" to "warming", started.steps[2].id to started.steps[2].state)

        val preview = ExtensionPreview("pv1", permissionsDigest = "d1", bundle = listOf(ExtensionPreview("pv2", permissionsDigest = "d2")))
        server.enqueue(MockResponse().setResponseCode(202).setBody("""{"operation":$op}"""))
        api.agree(preview)
        val agreed = server.takeRequest()
        assertEquals("POST /api/v1/extensions/-/install", "${agreed.method} ${agreed.path}")
        val sent = Json.parseToJsonElement(agreed.body.readUtf8()).jsonObject
        assertEquals(JsonPrimitive(true), sent["async"])
        assertEquals(JsonPrimitive("pv1"), sent["preview_id"])
        assertEquals(1, sent["bundle"]!!.jsonArray.size)

        server.enqueue(MockResponse().setResponseCode(202).setBody("""{"operation":$op}"""))
        api.agree(preview.copy(upgrade = true), upgradeOf = "example-notes")
        assertTrue(body(server).startsWith("POST /api/v1/extensions/example-notes/-/upgrade "))

        server.enqueue(MockResponse().setResponseCode(202).setBody("""{"operation":$op}"""))
        api.startUninstall("example-notes", keepData = true)
        assertEquals("DELETE /api/v1/extensions/example-notes/-/?async=1&keep_data=1 ", body(server))
        server.enqueue(MockResponse().setResponseCode(202).setBody("""{"operation":$op}"""))
        api.startUninstall("example-notes", keepData = false)
        assertEquals("DELETE /api/v1/extensions/example-notes/-/?async=1 ", body(server))
        // #5611: agreed to switch off its dependents, the removal in steps goes on with cascade.
        server.enqueue(MockResponse().setResponseCode(202).setBody("""{"operation":$op}"""))
        api.startUninstall("example-notes", keepData = true, cascade = true)
        assertEquals("DELETE /api/v1/extensions/example-notes/-/?async=1&keep_data=1&cascade=1 ", body(server))

        server.enqueue(MockResponse().setBody("""{"operations":[$op]}"""))
        assertEquals(listOf("0a1b2c"), api.operations().map { it.id })
        assertEquals("GET /api/v1/extensions/-/operations ", body(server))

        server.enqueue(MockResponse().setBody(op.replace("\"state\":\"running\",\"step\":\"download\"", "\"state\":\"needs_consent\",\"step\":\"consent\"")
            .replace("\"retryable\":false", "\"retryable\":false,\"preview\":{\"preview_id\":\"pv9\",\"permissions_digest\":\"d9\"}")))
        val waiting = api.operation("0a1b2c")
        assertEquals("GET /api/v1/extensions/-/operations/0a1b2c ", body(server))
        assertEquals("pv9", waiting.preview?.previewId)
        assertTrue(waiting.active)

        server.enqueue(MockResponse().setResponseCode(202).setBody("""{"operation":$op}"""))
        api.retryOperation("0a1b2c")
        assertEquals("POST /api/v1/extensions/-/operations/0a1b2c/retry {}", body(server))

        server.enqueue(MockResponse().setResponseCode(204))
        api.cancelOperation("0a1b2c")
        assertEquals("DELETE /api/v1/extensions/-/operations/0a1b2c ", body(server))

        // 409 once it installs: the hub's reason comes through.
        server.enqueue(MockResponse().setResponseCode(409).setBody("""{"code":"cannot_cancel","error":"已经在安装，不能取消"}"""))
        val refused = runCatching { api.cancelOperation("0a1b2c") }.exceptionOrNull() as HubFailure
        assertEquals(409, refused.status)
        server.takeRequest()

        // An id that is not the hub's never becomes a path.
        assertTrue(runCatching { api.operation("../x") }.exceptionOrNull() is IllegalArgumentException)
    }

    @Test fun thePushedEvent() {
        val event = ServerEvent.decode("""{"type":"extension_operation","event_seq":9,"operation":$op}""")
        assertEquals("example-notes", (event as ExtensionOperationEvent).operation.extension)
        // One without an id is not an operation.
        assertNull(ServerEvent.decode("""{"type":"extension_operation","operation":{"kind":"install"}}"""))
    }

    /** A pack with its members (#1154): `members=1` on both routes, the members removed, and a removal that stopped part way. */
    @Test fun aPackGoesWithItsMembersAndAPartialUninstallSaysWhichWent() = withHub { server, api ->
        server.enqueue(MockResponse().setBody("""{"ok":true,"removed_members":["example-notes","example-ui"]}"""))
        assertEquals(listOf("example-notes", "example-ui"), api.uninstall("example-pack", keepData = true, members = true).removedMembers)
        assertEquals("DELETE /api/v1/extensions/example-pack/-/?keep_data=1&members=1 ", body(server))
        // An older hub says nothing of members.
        server.enqueue(MockResponse().setBody("""{"ok":true}"""))
        assertEquals(null, api.uninstall("example-pack", keepData = false, members = true).removedMembers)
        assertEquals("DELETE /api/v1/extensions/example-pack/-/?members=1 ", body(server))
        server.enqueue(MockResponse().setResponseCode(202).setBody("""{"operation":${op.replace("\"kind\":\"install\"", "\"kind\":\"uninstall\"")}}"""))
        api.startUninstall("example-pack", keepData = true, cascade = true, members = true)
        assertEquals("DELETE /api/v1/extensions/example-pack/-/?async=1&keep_data=1&cascade=1&members=1 ", body(server))

        server.enqueue(MockResponse().setResponseCode(409).setBody("""{"ok":false,"code":"partial_uninstall",
            "error":"Removal didn’t finish. Removed members: example-notes. Not removed: example-ui. The pack is still installed, so you can try again.",
            "removed_members":["example-notes"],"failed_members":[{"id":"example-ui","error":"still running","detail":"busy"}],
            "remaining_members":["example-mail"]}"""))
        val partial = runCatching { api.uninstall("example-pack", members = true) }.exceptionOrNull() as ExtensionPartialUninstall
        assertEquals(listOf("example-notes"), partial.removed)
        assertEquals(listOf(ExtensionFailedMember("example-ui", "still running", "busy")), partial.failed)
        assertEquals(listOf("example-mail"), partial.remaining)
        assertTrue(partial.message!!.startsWith("Removal didn’t finish"))
        server.takeRequest()
        Unit
    }
}
