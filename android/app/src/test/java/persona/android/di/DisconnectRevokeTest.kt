package persona.android.di

import androidx.test.ext.junit.runners.AndroidJUnit4
import java.net.InetAddress
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import okhttp3.mockwebserver.Dispatcher
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.RecordedRequest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.Endpoint
import persona.core.contracts.api.ConnectionState
import persona.core.hub.HubConnectionConfig
import persona.core.hub.HubNetwork
import persona.core.hub.OkHttpHubConnection

/**
 * Disconnecting on Android revokes the device's credential on the hub (#5538, as Apple's HubSignOut.swift #1824):
 * only a revoked credential makes the hub drop the device's pushes and Live Activities.
 */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
class DisconnectRevokeTest {
    private class Hub : AutoCloseable {
        val requests = mutableListOf<RecordedRequest>()
        val server = MockWebServer().apply {
            dispatcher = object : Dispatcher() {
                override fun dispatch(request: RecordedRequest): MockResponse {
                    return when (request.requestUrl!!.encodedPath) {
                        "/api/v1/health" -> MockResponse().setBody("""{"ok":true,"hub_id":"test-hub"}""")
                        "/api/v1/ws" -> MockResponse().withWebSocketUpgrade(object : WebSocketListener() {
                            override fun onOpen(webSocket: WebSocket, response: Response) {
                                webSocket.send("""{"type":"hello","hub_id":"test-hub","features":[],"endpoints":[]}""")
                            }
                        })
                        "/api/v1/auth/tokens/device-123" -> {
                            requests += request
                            MockResponse().setBody("""{"ok":true}""")
                        }
                        else -> MockResponse().setResponseCode(404)
                    }
                }
            }
            start(InetAddress.getLoopbackAddress(), 0)
        }
        override fun close() = server.close()
    }

    private fun connection(hub: Hub) = OkHttpHubConnection(
        HubConnectionConfig("test-hub", null, "test-token", listOf(Endpoint(hub.server.url("/").toString(), "lan")), "test", deviceId = "device-123"),
        HubNetwork("network"))

    @Test fun revokeCredentialCallsDeleteWithTheDeviceCredentialId() = runBlocking {
        Hub().use { hub -> connection(hub).use { connection ->
            withTimeout(8000) { connection.state.first { it == ConnectionState.Online } }
            revokeCredential(connection, "device-123")
            assertEquals(1, hub.requests.size)
            assertEquals("DELETE", hub.requests[0].method)
            assertEquals("/api/v1/auth/tokens/device-123", hub.requests[0].path)
        }}
    }

    @Test fun revokeCredentialWithBlankDeviceIdDoesNothing() = runBlocking {
        Hub().use { hub -> connection(hub).use { connection ->
            withTimeout(8000) { connection.state.first { it == ConnectionState.Online } }
            revokeCredential(connection, "")
            assertTrue(hub.requests.isEmpty())
        }}
    }
}
