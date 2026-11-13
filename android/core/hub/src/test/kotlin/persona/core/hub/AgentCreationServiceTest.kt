package persona.core.hub

import persona.core.contracts.JsonCodec
import persona.core.contracts.api.*
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.*
import okhttp3.OkHttpClient
import okhttp3.mockwebserver.MockResponse
import persona.core.hub.ProtocolCompatibilityMockServer as MockWebServer
import okhttp3.mockwebserver.SocketPolicy
import org.junit.Assert.*
import org.junit.Test
import java.util.Base64

class AgentCreationServiceTest {
    @Test fun codexCapabilityDecodesBothLauncherShapesAndStartsWithNoPromptOrAsync() = runBlocking {
        MockWebServer().use { server ->
            val service = HttpAgentCreationService("hub/pairing", server.url("/").toString(), "test-token", OkHttpClient())
            server.enqueue(MockResponse().setBody("""{"launchers":[{"name":"modern","agent":"codex","prompt_optional":true},{"name":"older","agent_type":"codex"}]}"""))
            val launchers = service.launchers("computer")
            assertEquals(listOf("codex", "codex"), launchers.map { it.agentType })
            assertTrue(launchers.first().promptOptional)
            assertFalse(launchers.last().promptOptional)
            server.takeRequest()
            server.enqueue(MockResponse().setBody("""{"session":{"machine_id":"computer","session_id":"waiting","agent":"codex","status":"idle","pane_id":"pane","awaiting_first_message":true}}"""))
            val result = service.start(AgentStartRequest("computer", "/code", "modern", agentType = "codex"),
                "00000000-0000-4000-8000-000000000004") as AgentStartResult.Created
            val body = JsonCodec.parseToJsonElement(server.takeRequest().body.readUtf8()).jsonObject
            assertFalse("prompt" in body)
            assertFalse("async" in body)
            assertTrue(result.session.awaitingFirstMessage)
            assertEquals("idle", result.session.status)
        }
    }

    @Test fun launcherTypesMatchWebPrecedenceRegardlessOfKeyOrder() {
        for (fields in listOf(
            """"agent":"codex","agent_type":"claude"""",
            """"agent_type":"claude","agent":"codex"""",
        )) assertEquals("claude", JsonCodec.decodeFromString<AgentLauncher>("""{"name":"configured",$fields}""").agentType)
        for (fields in listOf(
            """"agent":"codex","agent_type":""""",
            """"agent_type":"","agent":"codex"""",
            """"agent_type":null,"agent":"codex"""",
            """"agent":"codex"""",
        )) assertEquals("codex", JsonCodec.decodeFromString<AgentLauncher>("""{"name":"fallback",$fields}""").agentType)
        assertEquals("claude", JsonCodec.decodeFromString<AgentLauncher>("""{"name":"default","agent_type":"","agent":null}""").agentType)
    }

    @Test fun lostStartResponseQueriesTheReceiptWithoutPostingAgain() = runBlocking {
        MockWebServer().use { server ->
            val service = HttpAgentCreationService("hub/pairing", server.url("/").toString(), "test-token", OkHttpClient())
            server.enqueue(MockResponse().setBody("""{"launchers":[{"name":"legacy"},{"name":"wrapper","agent_type":"codex"}]}"""))
            val launchers = service.launchers("machine")
            assertEquals(listOf("claude", "codex"), (launchers.map { it.agentType }).zhAny())
            server.takeRequest()
            val operation = "00000000-0000-4000-8000-000000000002"
            val saved = Base64.getEncoder().encodeToString("""{"session":{"machine_id":"machine","session_id":"created","agent":"codex"}}""".toByteArray())
            server.enqueue(MockResponse().setSocketPolicy(SocketPolicy.DISCONNECT_AFTER_REQUEST))
            server.enqueue(MockResponse().setBody("""{"kind":"start","state":"done","result":{"status":200,"body":"$saved"}}"""))
            val result = service.start(AgentStartRequest("machine", "/code", "wrapper", agentType = "codex"), operation)
            assertEquals("created", ((result as AgentStartResult.Created).session.sessionId).zhAny())
            val post = server.takeRequest()
            assertEquals("POST", (post.method).zhAny())
            assertEquals(operation, post.getHeader("X-Persona-Op-Id"))
            assertEquals("codex", (JsonCodec.parseToJsonElement(post.body.readUtf8()).jsonObject["agent_type"]?.jsonPrimitive?.content).zhAny())
            val lookup = server.takeRequest()
            assertEquals("GET", (lookup.method).zhAny())
            assertEquals("/api/v1/ops/$operation", (lookup.path).zhAny())
            assertEquals(3, server.requestCount)
        }
    }

    @Test fun directoryEncodingAndStartReceiptPreserveTheOperation() = runBlocking {
        MockWebServer().use { server ->
            val service = HttpAgentCreationService("hub/pairing", server.url("/").toString(), "test-token", OkHttpClient())
            server.enqueue(MockResponse().setBody("""{"path":"/code/a & b","entries":[],"recent":[]}"""))
            assertEquals("/code/a & b", (service.directories("machine/one", "/code/a & b").path).zhAny())
            val dirs = server.takeRequest()
            assertEquals("/api/v1/machines/machine%2Fone/dirs", (dirs.requestUrl?.encodedPath).zhAny())
            assertEquals("/code/a & b", (dirs.requestUrl?.queryParameter("path")).zhAny())

            val operation = "00000000-0000-4000-8000-000000000001"
            server.enqueue(MockResponse().setResponseCode(202).setBody("""{"state":"pending"}"""))
            assertEquals(AgentStartResult.Pending, service.start(AgentStartRequest("machine", "/code", "claude", "首句", "plan", "high", resumeSessionId = "history"), operation))
            val start = server.takeRequest()
            assertEquals("/api/v1/sessions", (start.path).zhAny())
            assertEquals(operation, start.getHeader("X-Persona-Op-Id"))
            assertEquals("Bearer test-token", (start.getHeader("Authorization")).zhAny())
            val body = JsonCodec.parseToJsonElement(start.body.readUtf8()).jsonObject
            assertEquals("plan", (body["permission_mode"]?.jsonPrimitive?.content).zhAny())
            assertEquals("high", (body["effort"]?.jsonPrimitive?.content).zhAny())
            assertEquals("首句", (body["prompt"]?.jsonPrimitive?.content).zhAny())
            assertEquals("history", (body["resume_session_id"]?.jsonPrimitive?.content).zhAny())

            val saved = Base64.getEncoder().encodeToString("""{"session":{"machine_id":"machine","session_id":"session"}}""".toByteArray())
            server.enqueue(MockResponse().setBody("""{"state":"done","result":{"status":200,"body":"$saved"}}"""))
            val result = service.operation(operation) as AgentStartResult.Created
            assertEquals("session", (result.session.sessionId).zhAny())
            assertEquals("GET", (server.takeRequest().method).zhAny())
            server.enqueue(MockResponse().setResponseCode(404).setBody("{}"))
            assertEquals(AgentStartResult.Unknown, service.operation(operation))
            assertEquals("/api/v1/ops/$operation", (server.takeRequest().path).zhAny())
        }
    }

    /** The manager refuses a provider outside the computer's scope before anything starts: its words are the answer, no lookup. */
    @Test fun aStartRefusedForTheProvidersScopeSaysWhy() = runBlocking {
        MockWebServer().use { server ->
            val service = HttpAgentCreationService("hub/pairing", server.url("/").toString(), "test-token", OkHttpClient())
            server.enqueue(MockResponse().setResponseCode(409).setBody("""{"error":"jv_gw is not set to run on Laptop.","code":"launcher_out_of_scope"}"""))
            val result = service.start(AgentStartRequest("machine", "/code", "jv_gw"), "00000000-0000-4000-8000-000000000003")
            assertTrue(result is AgentStartResult.Rejected)
            assertEquals(1, server.requestCount)
        }
    }
}
