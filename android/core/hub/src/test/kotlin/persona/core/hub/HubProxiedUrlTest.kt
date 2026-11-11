package persona.core.hub

import persona.core.contracts.Endpoint
import persona.core.contracts.api.PairingRequest
import java.net.InetAddress
import java.security.MessageDigest
import java.util.Base64
import kotlinx.coroutines.runBlocking
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.mockwebserver.Dispatcher
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.RecordedRequest
import okhttp3.tls.HandshakeCertificates
import okhttp3.tls.HeldCertificate
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * A pairing link with `fp` whose `url` is served by a reverse proxy or tunnel with a public CA's certificate (#2622,
 * docs/operations/network.md "Reverse-proxy subpaths (Android)"): that origin alone is checked with system trust and its
 * host name; every other endpoint stays pinned; a refusal there names the address instead of reading as a changed hub key.
 */
class HubProxiedUrlTest {
    private val loopback = InetAddress.getLoopbackAddress().hostAddress!!
    /** The proxy's certificate, from the "public CA" the tests' system trust holds. */
    private val proxyCert = HeldCertificate.Builder().addSubjectAlternativeName(loopback).build()
    /** The hub's own TLS listener's key, the pairing's `fp`. */
    private val hubCert = HeldCertificate.Builder().addSubjectAlternativeName(loopback).build()
    private val publicCa = { HandshakeCertificates.Builder().addTrustedCertificate(proxyCert.certificate).build().trustManager }

    @Test fun onlyTheLinksHttpsUrlOnAHostNameOffTheTlsPortIsSystemTrusted() {
        fun origin(url: String, fp: String? = "A".repeat(43), tls: Int? = 19443) =
            HubPairingClient.systemTrustedOrigin(PairingRequest(url, "code", fp = fp, tls = tls))
        assertEquals("hub.example.com:443", origin("https://hub.example.com"))
        assertEquals("hub.example.com:8443", origin("https://Hub.Example.com:8443/persona"))
        // the hub's own listener, an address, plain HTTP: pinned or not HTTPS
        assertNull(origin("https://hub.example.com:19443"))
        assertNull(origin("https://203.0.113.5"))
        assertNull(origin("https://[2001:db8::a]"))
        assertNull(origin("http://hub.example.com"))
        // without `fp` everything HTTPS is system-trusted already; without `tls`, `url` is the hub's own listener
        assertNull(origin("https://hub.example.com", fp = null))
        assertNull(origin("https://hub.example.com", tls = null))
    }

    @Test fun theProxiedOriginUsesSystemTrustAndNoOtherEndpointDoes() = runBlocking {
        tls(proxyCert).use { proxy ->
            tls(proxyCert).use { elsewhere ->
                tls(hubCert).use { listener ->
                    HubHttpClient("test-hub", fingerprint(hubCert), "test", systemTrust = publicCa,
                        systemOrigins = setOf(origin(proxy)), pins = setOf(fingerprint(HeldCertificate.Builder().build()))).use { client ->
                        assertTrue(client.systemTrusts(endpoint(proxy).url.toHttpUrl()))
                        // the proxy's CA certificate passes there, and its hub id is still checked (a group's pin check is skipped)
                        assertEquals("test-hub", client.health(endpoint(proxy)).hubId)
                        // the same CA certificate on any other origin is refused: that endpoint is pinned
                        val pinned = failure { client.health(endpoint(elsewhere)) }
                        assertEquals(HubError.CertificateMismatch, pinned.kind)
                        assertNull(pinned.code)
                        // the hub's own listener still answers with its pinned key
                        assertEquals("test-hub", client.health(endpoint(listener)).hubId)
                    }
                }
            }
        }
    }

    @Test fun aRefusedProxyCertificateNamesTheAddress() = runBlocking {
        val wrongName = HeldCertificate.Builder().addSubjectAlternativeName("hub.example.com").build()
        val untrusted = HeldCertificate.Builder().addSubjectAlternativeName(loopback).build()
        for ((served, trust) in listOf(wrongName to wrongName, untrusted to proxyCert)) {
            tls(served).use { proxy ->
                HubHttpClient("test-hub", fingerprint(hubCert), "test",
                    systemTrust = { HandshakeCertificates.Builder().addTrustedCertificate(trust.certificate).build().trustManager },
                    systemOrigins = setOf(origin(proxy))).use { client ->
                    val failure = failure { client.health(endpoint(proxy)) }
                    assertEquals(HubError.CertificateMismatch, failure.kind)
                    assertEquals(PROXY_CERTIFICATE, failure.code)
                    assertEquals(endpoint(proxy).url, failure.detail)
                }
            }
        }
    }

    @Test fun pairingGoesThroughTheProxiedUrlAndKeepsItsOrigin() = runBlocking {
        tls(proxyCert).use { proxy ->
            val url = endpoint(proxy).url
            HubPairingClient("test-hub", fingerprint(hubCert), listOf(url), "test",
                systemOrigins = setOf(origin(proxy)), systemTrust = publicCa).use { client ->
                val paired = client.pair("pairing-code", "phone") {}
                assertEquals("device-token", paired.token)
                assertEquals(fingerprint(hubCert), paired.fingerprint)
                assertEquals(setOf(origin(proxy)), paired.systemOrigins)
            }
        }
    }

    @Test fun aPinnedMismatchStillOutranksTheProxysCertificate() = runBlocking {
        val bad = HeldCertificate.Builder().addSubjectAlternativeName("hub.example.com").build()
        val stranger = HeldCertificate.Builder().addSubjectAlternativeName(loopback).build()
        tls(bad).use { proxy ->
            // the only certificate failure is the proxy's: it is named
            HubPairingClient("test-hub", fingerprint(hubCert), listOf(endpoint(proxy).url, "https://$loopback:1"), "test",
                systemOrigins = setOf(origin(proxy)), systemTrust = publicCa).use { client ->
                val failure = failure { client.pair("pairing-code", "phone") {} }
                assertEquals(PROXY_CERTIFICATE, failure.code)
            }
            // a pinned endpoint presenting another key says more: that one is reported
            tls(stranger).use { listener ->
                HubPairingClient("test-hub", fingerprint(hubCert), listOf(endpoint(proxy).url, endpoint(listener).url), "test",
                    systemOrigins = setOf(origin(proxy)), systemTrust = publicCa).use { client ->
                    val failure = failure { client.pair("pairing-code", "phone") {} }
                    assertEquals(HubError.CertificateMismatch, failure.kind)
                    assertNull(failure.code)
                }
            }
        }
    }

    private fun tls(certificate: HeldCertificate) = MockWebServer().apply {
        useHttps(HandshakeCertificates.Builder().heldCertificate(certificate).build().sslSocketFactory(), false)
        dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse = MockResponse().setBody(when (request.path!!.substringBefore('?')) {
                "/api/v1/health" -> """{"ok":true,"hub_id":"test-hub"}"""
                "/api/v1/auth/tokens" -> """{"token":"device-token","id":"device"}"""
                "/api/v1/machines" -> """{"machines":[]}"""
                "/api/v1/endpoints" -> """{"hub_id":"test-hub","endpoints":[]}"""
                else -> "{}"
            })
        }
        start(InetAddress.getLoopbackAddress(), 0)
    }

    private fun endpoint(server: MockWebServer) = Endpoint(server.url("/").newBuilder().host(loopback).build().toString().trimEnd('/'), "name")
    private fun origin(server: MockWebServer) = tlsKey(loopback, server.port)

    private fun fingerprint(certificate: HeldCertificate) = Base64.getUrlEncoder().withoutPadding().encodeToString(
        MessageDigest.getInstance("SHA-256").digest(certificate.certificate.publicKey.encoded))

    private suspend fun failure(block: suspend () -> Unit): HubFailure {
        try { block() } catch (error: Exception) { return HubFailure.from(error) }
        throw AssertionError("Expected a failure")
    }
}
