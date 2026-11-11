package persona.core.hub.tunnel

import kotlin.coroutines.CoroutineContext
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.async
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import org.junit.Assert.assertEquals
import org.junit.Test
import persona.core.contracts.Endpoint
import persona.core.contracts.api.LocalProxyRoute
import persona.core.contracts.api.TunnelKind
import persona.core.hub.HubHttpClient
import persona.core.hub.LoopbackHub

/** A replacement must not reuse a listener whose previous start is still being cancelled. */
class HubWaysTest {
    private class ManualDispatcher : CoroutineDispatcher() {
        private val tasks = ArrayDeque<Runnable>()
        val size: Int get() = synchronized(tasks) { tasks.size }

        override fun dispatch(context: CoroutineContext, block: Runnable) {
            synchronized(tasks) { tasks.addLast(block) }
        }

        suspend fun awaitTask(minimum: Int = 1) = withTimeout(5000) { while (size < minimum) delay(1) }

        fun runLast() {
            val task = synchronized(tasks) { tasks.removeLast() }
            task.run()
        }

        suspend fun runLastWhenReady(minimum: Int = 1) {
            awaitTask(minimum)
            runLast()
        }

        fun drain() {
            while (size > 0) runLast()
        }
    }

    @Test(timeout = 10_000) fun aReplacementDoesNotReuseAHalfStartedListener() = runBlocking {
        LoopbackHub(hubId = "hub-a", version = "1").use { hub ->
            FakeSocks5().use { proxy ->
                val dispatcher = ManualDispatcher()
                val identity = SshIdentity(SshIdentity.generateSeed(), "persona-test")
                HubWays(identity = { identity }, dispatcher = dispatcher).use { ways ->
                    val tunnel = HubTunnel("hub-a", hub.paired, proxy = LocalProxyRoute(port = proxy.port))

                    suspend fun openAndFinish(route: HubTunnel): String {
                        val result = async(start = CoroutineStart.UNDISPATCHED) { ways.open(route) }
                        dispatcher.runLastWhenReady()
                        dispatcher.runLastWhenReady()
                        return result.await()
                    }

                    // Keep a preferred port, then hold the next start after its listener is up but before its final checks.
                    openAndFinish(tunnel)
                    ways.close("hub-a", TunnelKind.Proxy, forget = false)
                    val oldStart = async(start = CoroutineStart.UNDISPATCHED) { runCatching { ways.open(tunnel) } }
                    dispatcher.runLastWhenReady()
                    dispatcher.awaitTask()

                    // Change settings twice so the final open is the same way, then run it before the cancelled cleanup.
                    val replacement = HubTunnel("hub-a", hub.paired,
                        proxy = LocalProxyRoute(port = proxy.port, targetPort = java.net.URI(hub.paired).port))
                    val superseded = async(start = CoroutineStart.UNDISPATCHED) { runCatching { ways.open(replacement) } }
                    val reopened = async(start = CoroutineStart.UNDISPATCHED) { ways.open(tunnel) }
                    dispatcher.runLastWhenReady()
                    // Two earlier continuations stay queued while the replacement's own continuation arrives third.
                    dispatcher.runLastWhenReady(minimum = 3)
                    val base = reopened.await()
                    dispatcher.drain()
                    oldStart.join()
                    superseded.join()

                    HubHttpClient("hub-a", null, "test").use { client ->
                        assertEquals("hub-a", client.health(Endpoint(base, "proxy"), fresh = true).hubId)
                    }
                }
            }
        }
    }
}
