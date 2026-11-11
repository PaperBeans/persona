package persona.core.hub.tunnel

import java.net.InetAddress
import java.net.InetSocketAddress
import java.net.URI
import java.net.ServerSocket
import java.util.concurrent.TimeUnit
import java.util.concurrent.CountDownLatch
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.atomic.AtomicReference
import kotlin.concurrent.thread
import okhttp3.OkHttpClient
import okhttp3.Request
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test
import persona.core.contracts.api.LocalProxyRoute
import persona.core.contracts.api.TunnelException
import persona.core.contracts.api.TunnelProblem
import persona.core.hub.LoopbackHub
import persona.core.hub.closedPort

/** docs/operations/network.md "Connecting to hub through a local SOCKS5 proxy": "Test connection and save" and the forward. */
class LocalProxyForwardTest {
    private val LoopbackHub.port get() = URI(paired).port

    /**
     * A free port below every system's ephemeral range (Linux 32768-60999, macOS and Windows 49152-65535). A port bound to 0
     * is an ephemeral one: once closed, the system can give it to any process's next outgoing connection, so a test that
     * closes it and asks for it back fails whenever something else on the machine connects in between (#3189).
     */
    private fun listenablePort(): Int = (20_000 until 32_000).shuffled().first { port ->
        runCatching { ServerSocket().use { it.reuseAddress = true; it.bind(InetSocketAddress(InetAddress.getByName("127.0.0.1"), port)) } }.isSuccess
    }

    private fun problem(block: () -> Unit): TunnelProblem {
        try { block() } catch (error: TunnelException) { return error.problem }
        fail("No TunnelException")
        throw AssertionError()
    }

    @Test fun theHubAnswersThroughTheProxyAtThePairedAddress() {
        LoopbackHub(hubId = "hub-a", version = "1").use { hub ->
            FakeSocks5().use { proxy ->
                LocalProxyForward.verify(LocalProxyRoute(port = proxy.port), hub.paired, "hub-a")
                assertTrue(proxy.asked.all { it == "127.0.0.1" to hub.port })
                assertEquals(2, proxy.asked.size) // the check, then the health request
            }
        }
    }

    @Test fun aTargetSetByHandIsAskedForByName() {
        LoopbackHub(hubId = "hub-a", version = "1").use { hub ->
            FakeSocks5(target = InetSocketAddress(InetAddress.getLoopbackAddress(), hub.port)).use { proxy ->
                LocalProxyForward.verify(LocalProxyRoute(port = proxy.port, targetHost = "hub.internal", targetPort = 18660),
                    "https://hub.example.com/", "hub-a")
                assertEquals("hub.internal" to 18660, proxy.asked.first())
            }
        }
    }

    @Test fun anHttpsPairingIsReachedAtTheHubsHttpPort() {
        assertEquals("hub.example.com" to LocalProxyRoute.HUB_HTTP_PORT, LocalProxyRoute(port = 1).target("https://hub.example.com/"))
        assertEquals("192.0.2.10" to 80, LocalProxyRoute(port = 1).target("http://192.0.2.10/"))
    }

    @Test fun aLoginIsAskedForAndChecked() {
        LoopbackHub(hubId = "hub-a", version = "1").use { hub ->
            FakeSocks5(login = "me" to "secret").use { proxy ->
                assertEquals(TunnelProblem.NoAcceptableMethod(loginOffered = false),
                    problem { LocalProxyForward.verify(LocalProxyRoute(port = proxy.port), hub.paired, "hub-a") })
                assertEquals(TunnelProblem.LoginRejected, problem {
                    LocalProxyForward.verify(LocalProxyRoute(port = proxy.port, username = "me", password = "wrong"), hub.paired, "hub-a")
                })
                LocalProxyForward.verify(LocalProxyRoute(port = proxy.port, username = "me", password = "secret"), hub.paired, "hub-a")
            }
        }
    }

    @Test fun whatIsBehindTheProxyIsTold() {
        LoopbackHub(hubId = "hub-a", version = "1").use { hub ->
            FakeSocks5().use { proxy ->
                val target = "127.0.0.1:${hub.port}"
                assertEquals(TunnelProblem.OtherHub(target), problem { LocalProxyForward.verify(LocalProxyRoute(port = proxy.port), hub.paired, "hub-b") })
                hub.healthy = false
                assertEquals(TunnelProblem.NotAHub(target), problem { LocalProxyForward.verify(LocalProxyRoute(port = proxy.port), hub.paired, "hub-a") })
                proxy.reply = 5
                assertEquals(TunnelProblem.ConnectFailed(5), problem { LocalProxyForward.verify(LocalProxyRoute(port = proxy.port), hub.paired, "hub-a") })
            }
        }
        LoopbackHub(hubId = "hub-a").use { hub ->
            FakeSocks5().use { proxy ->
                assertEquals(TunnelProblem.SeenAsInternet("127.0.0.1:${hub.port}"),
                    problem { LocalProxyForward.verify(LocalProxyRoute(port = proxy.port), hub.paired, "hub-a") })
            }
        }
    }

    @Test fun aProxyThatIsNotThereOrNotSocks5IsTold() {
        assertTrue(problem { LocalProxyForward.verify(LocalProxyRoute(port = closedPort()), "http://127.0.0.1:1/", null) }
            is TunnelProblem.ProxyUnreachable)
        FakeSocks5(http = true).use { proxy ->
            assertEquals(TunnelProblem.NotSocks5, problem { LocalProxyForward.verify(LocalProxyRoute(port = proxy.port), "http://127.0.0.1:1/", null) })
        }
    }

    @Test fun theForwardCarriesEveryConnectionAndTakesThePortItHadBefore() {
        LoopbackHub(hubId = "hub-a", version = "1").use { hub ->
            FakeSocks5().use { proxy ->
                val dial = LocalProxyForward.Dial.of(LocalProxyRoute(port = proxy.port), hub.paired)
                val client = OkHttpClient.Builder().proxy(java.net.Proxy.NO_PROXY).build()
                val port = LocalProxyForward(dial).use { forward ->
                    val port = forward.listen(preferred = listenablePort())
                    repeat(3) {
                        val request = Request.Builder().url("http://127.0.0.1:$port/api/v1/health").header("Connection", "close").build()
                        client.newCall(request).execute().use { response -> assertEquals(200, response.code) }
                    }
                    port
                }
                LocalProxyForward(dial).use { assertEquals(port, it.listen(preferred = port)) }
                assertEquals(3, proxy.carried.get())
            }
        }
    }

    @Test fun closeWaitsUntilTheListenerWorkerHasExited() {
        val dial = LocalProxyForward.Dial("127.0.0.1", 1, "127.0.0.1", 1)
        repeat(100) {
            LocalProxyForward(dial).use { forward ->
                val port = forward.listen()
                val worker = Thread.getAllStackTraces().keys.single { it.name == "Local proxy forward $port" }
                val deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(1)
                while (worker.stackTrace.none { it.methodName == "accept" }) {
                    assertTrue("Listener did not enter accept", System.nanoTime() < deadline)
                    Thread.yield()
                }
                forward.close()
                assertFalse("close returned while the listener still owned its socket", worker.isAlive)
            }
        }
    }

    @Test fun anOccupiedPreferredPortFallsBackWithoutTouchingItsOwner() {
        val loopback = InetAddress.getByName("127.0.0.1")
        ServerSocket(0, 1, loopback).use { occupied ->
            val dial = LocalProxyForward.Dial("127.0.0.1", 1, "127.0.0.1", 1)
            LocalProxyForward(dial).use { forward ->
                val port = forward.listen(occupied.localPort)
                assertTrue(port > 0 && port != occupied.localPort)
                assertFalse(occupied.isClosed)
            }
        }
    }


    @Test fun closeCanRaceWithOpeningWithoutLeavingAListener() {
        val dial = LocalProxyForward.Dial("127.0.0.1", 1, "127.0.0.1", 1)
        repeat(100) {
            val forward = LocalProxyForward(dial)
            val start = CountDownLatch(1)
            val port = AtomicInteger()
            val failure = AtomicReference<Throwable?>()
            fun worker(action: () -> Unit) = thread(isDaemon = true) {
                try { start.await(); action() } catch (error: Throwable) { failure.set(error) }
            }
            val opening = worker { port.set(forward.listen()) }
            val closing = worker { forward.close() }
            start.countDown()
            val workerDeadlineMillis = TimeUnit.SECONDS.toMillis(2)
            opening.join(workerDeadlineMillis)
            closing.join(workerDeadlineMillis)
            assertFalse("Opening did not finish", opening.isAlive)
            assertFalse("Closing did not finish", closing.isAlive)
            failure.get()?.let { throw AssertionError("Concurrent lifecycle failed", it) }
            assertFalse(forward.listening)
            if (port.get() > 0) ServerSocket().use { released ->
                released.reuseAddress = true
                released.bind(InetSocketAddress("127.0.0.1", port.get()))
            }
        }
    }


    @Test fun closePreservesInterruptionWithoutThrowingToItsCaller() {
        val dial = LocalProxyForward.Dial("127.0.0.1", 1, "127.0.0.1", 1)
        val forward = LocalProxyForward(dial)
        forward.listen()
        try {
            Thread.currentThread().interrupt()
            forward.close()
            assertTrue(Thread.currentThread().isInterrupted)
            assertFalse(forward.listening)
        } finally {
            Thread.interrupted()
            forward.close()
        }
    }

}
