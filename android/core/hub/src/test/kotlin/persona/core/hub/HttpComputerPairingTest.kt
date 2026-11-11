package persona.core.hub

import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.JsonObject
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.mockwebserver.MockResponse
import persona.core.hub.ProtocolCompatibilityMockServer as MockWebServer
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import persona.core.contracts.JsonCodec
import persona.core.contracts.api.ComputerOs

/**
 * Adding a computer to an assistant (docs/protocol/protocol.md "Computer pairing codes"): `add.manager` names it, the
 * reply's `manager` says where the computer is now, and a hub from before the field sends none.
 */
class HttpComputerPairingTest {
    private val reply = """{"token":"jp_x","expires_at":"2026-11-06T02:10:00Z","machine":"macbook","name":"MacBook","trust":"pending",
        "added":true,"pair_url":"persona://link?machine=macbook""""

    private fun api(server: MockWebServer): HttpComputerPairing {
        val http = OkHttpClient()
        return HttpComputerPairing({ path, method, body -> http.newCall(Request.Builder().url(server.url(path)).method(method, body).build()).execute() })
    }

    private fun sent(server: MockWebServer) = JsonCodec.parseToJsonElement(server.takeRequest().body.readUtf8()) as JsonObject

    @Test fun theManagerIsSentAndTheReplysIsRead() = runBlocking<Unit> {
        MockWebServer().use { server ->
            val api = api(server)
            server.enqueue(MockResponse().setBody("$reply,\"manager\":\"m-3fa21c\"}"))
            val code = api.add("MacBook", ComputerOs.Mac, "m-3fa21c")
            assertEquals("""{"add":{"name":"MacBook","os":"mac","manager":"m-3fa21c"}}""", sent(server).toString())
            assertEquals("m-3fa21c", code.manager)
            // The default assistant is named too, by its real id.
            server.enqueue(MockResponse().setBody("$reply,\"manager\":\"assistant\"}"))
            assertEquals("assistant", api.add("MacBook", ComputerOs.Mac, "assistant").manager)
            assertEquals("""{"add":{"name":"MacBook","os":"mac","manager":"assistant"}}""", sent(server).toString())
            // The hub put it elsewhere: the reply says so.
            server.enqueue(MockResponse().setBody("$reply,\"manager\":\"assistant\"}"))
            assertEquals("assistant", api.add("MacBook", ComputerOs.Linux, "m-3fa21c").manager)
            server.takeRequest()
        }
    }

    @Test fun noManagerNamedAndAnOlderHubsReply() = runBlocking<Unit> {
        MockWebServer().use { server ->
            val api = api(server)
            // None named (a hub without a roster): the body as before.
            server.enqueue(MockResponse().setBody("$reply}"))
            val code = api.add("MacBook", ComputerOs.Mac)
            assertEquals("""{"add":{"name":"MacBook","os":"mac"}}""", sent(server).toString())
            // A hub from before add.manager answers without it.
            assertNull(code.manager)
            // Another code for a computer names no manager: it keeps its own, which the reply gives.
            server.enqueue(MockResponse().setBody("$reply,\"manager\":\"m-8b04d2\"}"))
            assertEquals("m-8b04d2", api.again("macbook").manager)
            assertEquals("""{"machine":"macbook"}""", sent(server).toString())
        }
    }

    @Test fun theHubsRefusalsKeepTheirCodeAndWords() = runBlocking<Unit> {
        MockWebServer().use { server ->
            val api = api(server)
            for ((status, code) in listOf(404 to "no_manager", 409 to "installing", 503 to "assistant_offline", 503 to "owners_unknown")) {
                server.enqueue(MockResponse().setResponseCode(status).setBody("""{"code":"$code","error":"hub words for $code"}"""))
                val refused = runCatching { api.add("MacBook", ComputerOs.Mac, "m-3fa21c") }.exceptionOrNull() as HubFailure
                assertEquals(code, refused.code)
                assertEquals(status, refused.status)
                assertEquals("hub words for $code", refused.message)
                server.takeRequest()
            }
        }
    }
}
