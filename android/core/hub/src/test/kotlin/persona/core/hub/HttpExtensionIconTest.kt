package persona.core.hub

import kotlinx.coroutines.async
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.flow.first
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.mockwebserver.MockResponse
import okio.Buffer
import org.junit.Assert.*
import org.junit.Test
import persona.core.contracts.api.ExtensionIconPath
import java.util.concurrent.TimeUnit

class HttpExtensionIconTest {
    private val installed = "/api/v1/extensions/notes/-/icon?v=1.0.0"
    private val catalog = "/api/v1/extensions/-/catalog/official/notes/icon?sha256=" + "a".repeat(64)

    @Test fun onlyExactRawRelativeOrSameOriginIconPathsAreAuthenticated() {
        val api = HttpExtensionManagement({ _, _, _ -> error("No request expected") }, iconBase = { "http://hub.example/prefix/" })
        for (path in listOf(installed, catalog)) {
            assertTrue(ExtensionIconPath.matches(path))
            assertEquals(path, api.iconPath(path))
            assertEquals(path, api.iconPath("http://hub.example/prefix$path"))
        }
        for (value in listOf("", "//hub.example$installed", installed + "&token=anything", installed + "#fragment",
            installed.replace("notes", ".."), installed.replace("notes", "%2e%2e"), installed.replace("notes", "notes/../notes"),
            installed.replace("notes", "notes%2fextra"), installed.replace("/-/", "/"),
            "http://other.example/prefix$installed", "https://hub.example/prefix$installed", "http://hub.example:81/prefix$installed",
            "http://user:pass@hub.example/prefix$installed", "http://hub.example/prefix/x/..$installed",
            "https://images.example/logo.png", "/api/v1/extensions/notes/-/image/signed")) assertNull(value, api.iconPath(value))
        assertTrue(HttpExtensionManagement.isImagePath("/api/v1/extensions/notes/-/image/signed"))
        assertFalse(HttpExtensionManagement.isImagePath(installed))
    }

    @Test fun downloadsBothRoutesWithExistingAuthAndRefusesOtherTargetsAndBadResponses() = runBlocking {
        ProtocolCompatibilityMockServer().use { server ->
            val http = OkHttpClient.Builder().followRedirects(false).retryOnConnectionFailure(false).build()
            val api = HttpExtensionManagement({ path, method, body ->
                http.newCall(Request.Builder().url(server.url(path)).header("Authorization", "Bearer synthetic-icon-token")
                    .method(method, body).build()).awaitResponse(buffered = false)
            }, iconBase = { server.url("/").toString() })
            for (path in listOf(installed, catalog)) {
                server.enqueue(MockResponse().setHeader("Content-Type", "image/png").setBody("fixture-image"))
                assertArrayEquals("fixture-image".toByteArray(), api.icon(path))
                val sent = server.takeRequest()
                assertEquals(path, sent.path)
                assertEquals("Bearer synthetic-icon-token", sent.getHeader("Authorization"))
                assertFalse(sent.path!!.contains("synthetic-icon-token"))
            }
            for (path in listOf("https://other.example$installed", "//other.example$installed", "/api/v1/extensions/notes/-/image/signed")) {
                assertTrue(runCatching { api.icon(path) }.exceptionOrNull() is IllegalArgumentException)
            }
            assertEquals(2, server.requestCount)
            for (response in listOf(
                MockResponse().setResponseCode(404),
                MockResponse().setHeader("Content-Type", "text/html").setBody("not an image"),
                MockResponse().setHeader("Content-Type", "image/png").setBody(""),
                MockResponse().setHeader("Content-Type", "image/png").setHeader("Content-Length", HttpExtensionManagement.MaxImageBytes + 1),
                MockResponse().setResponseCode(302).setHeader("Location", "https://other.example/logo.png"),
                MockResponse().setHeader("Content-Type", "image/png").setChunkedBody(Buffer().write(ByteArray((HttpExtensionManagement.MaxImageBytes + 1).toInt())), 8192),
            )) {
                server.enqueue(response)
                assertTrue(runCatching { api.icon(installed) }.exceptionOrNull() is HubFailure)
                server.takeRequest()
            }
        }
    }

    @Test fun productionConnectionStreamsIconsWithNormalAuthAndManagerOwnership() = runBlocking {
        iconConnection { connection, responses, requests ->
            val api = HttpExtensionManagement(connection, kotlinx.coroutines.flow.flowOf(true))
            for (path in listOf(installed, catalog)) {
                responses.add(MockResponse().setHeader("Content-Type", "image/png").setBody("fixture-image"))
                val bytes = kotlinx.coroutines.withContext(persona.core.contracts.api.RequestManager("work")) { api.icon(path) }
                assertArrayEquals("fixture-image".toByteArray(), bytes)
                val request = requests.take()
                assertEquals("Bearer synthetic-icon-token", request.getHeader("Authorization"))
                assertEquals("android/test", request.getHeader("X-Persona-Client"))
                assertEquals("work", request.requestUrl!!.queryParameter("manager"))
                assertEquals(path, request.requestUrl!!.newBuilder().removeAllQueryParameters("manager").build().let {
                    it.encodedPath + "?" + it.encodedQuery
                })
            }
        }
    }

    @Test fun productionConnectionRefusesOversizedStreamsBeforeTheirSlowTail() = runBlocking {
        iconConnection { connection, responses, requests ->
            val api = HttpExtensionManagement(connection, kotlinx.coroutines.flow.flowOf(true))
            responses.add(MockResponse().setHeader("Content-Type", "image/png")
                .setChunkedBody(Buffer().write(ByteArray(((2L * 1024 * 1024) * 2).toInt())), 8192)
                .throttleBody((2L * 1024 * 1024) + 16384, 3, TimeUnit.SECONDS))
            val failure = kotlinx.coroutines.withTimeout(2000) { runCatching { api.icon(installed) }.exceptionOrNull() }
            assertTrue("The bounded reader must fail before the delayed tail", failure is HubFailure)
            requests.take()
            responses.add(MockResponse().setHeader("Content-Type", "image/png").setBody("tail")
                .setHeader("Content-Length", (2L * 1024 * 1024) * 64).setBodyDelay(3, TimeUnit.SECONDS))
            val declared = kotlinx.coroutines.withTimeout(2000) { runCatching { api.icon(installed) }.exceptionOrNull() }
            assertTrue("Reject declared oversize before reading the body", declared is HubFailure)
            requests.take()
        }
    }

    private suspend fun iconConnection(block: suspend (OkHttpHubConnection, java.util.concurrent.LinkedBlockingQueue<MockResponse>, java.util.concurrent.LinkedBlockingQueue<okhttp3.mockwebserver.RecordedRequest>) -> Unit) {
        val certificate = okhttp3.tls.HeldCertificate.Builder().commonName("example.com").build()
        val responses = java.util.concurrent.LinkedBlockingQueue<MockResponse>()
        val requests = java.util.concurrent.LinkedBlockingQueue<okhttp3.mockwebserver.RecordedRequest>()
        val tls = okhttp3.tls.HandshakeCertificates.Builder().heldCertificate(certificate).build()
        okhttp3.mockwebserver.MockWebServer().use { server ->
            server.useHttps(tls.sslSocketFactory(), false)
            server.dispatcher = object : okhttp3.mockwebserver.Dispatcher() {
                override fun dispatch(request: okhttp3.mockwebserver.RecordedRequest): MockResponse {
                    return when (request.requestUrl!!.encodedPath) {
                        "/api/v1/health" -> MockResponse().setBody("""{"ok":true,"hub_id":"icon-hub"}""")
                        "/api/v1/ws" -> MockResponse().withWebSocketUpgrade(object : okhttp3.WebSocketListener() {
                            override fun onOpen(webSocket: okhttp3.WebSocket, response: okhttp3.Response) {
                                webSocket.send("""{"type":"hello","hub_id":"icon-hub","features":["extensions"]}""")
                            }
                        })
                        "/api/v1/managers" -> MockResponse().setBody("""{"managers":[],"default":"assistant"}""")
                        else -> { requests.add(request); responses.poll(5, TimeUnit.SECONDS) ?: MockResponse().setResponseCode(404) }
                    }
                }
            }
            server.start(java.net.InetAddress.getLoopbackAddress(), 0)
            val pin = java.util.Base64.getUrlEncoder().withoutPadding().encodeToString(
                java.security.MessageDigest.getInstance("SHA-256").digest(certificate.certificate.publicKey.encoded))
            OkHttpHubConnection(HubConnectionConfig("icon-hub", pin, "synthetic-icon-token",
                listOf(persona.core.contracts.Endpoint(server.url("/").toString(), "lan")), "test"),
                HubNetwork("offline", available = false)).use { connection ->
                connection.updateNetwork(HubNetwork("lan"))
                kotlinx.coroutines.withTimeout(5000) { connection.route.first { it != null } }
                try { block(connection, responses, requests) }
                finally { persona.core.contracts.api.AssistantManagerChoice.reset(); persona.core.contracts.api.AssistantManagerChoice.forgetItems() }
            }
        }
    }

    @Test fun cancelledIconDownloadCancelsTheExistingHttpCall() = runBlocking {
        ProtocolCompatibilityMockServer().use { server ->
            val http = OkHttpClient.Builder().readTimeout(5, TimeUnit.SECONDS).build()
            var call: okhttp3.Call? = null
            val api = HttpExtensionManagement({ path, _, _ ->
                http.newCall(Request.Builder().url(server.url(path)).build()).also { call = it }.awaitResponse(buffered = false)
            })
            server.enqueue(MockResponse().setHeadersDelay(2, TimeUnit.SECONDS).setHeader("Content-Type", "image/png").setBody("fixture"))
            val job = async { api.icon(installed) }
            kotlinx.coroutines.yield()
            assertNotNull(server.takeRequest(2, TimeUnit.SECONDS))
            job.cancelAndJoin()
            assertTrue(call!!.isCanceled())
        }
    }
}
