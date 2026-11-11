package persona.platform.notify

import androidx.work.ListenableWorker
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.IOException

class ConnectionWatchdogResultTest {
    private class Calls(var startResult: Boolean = true) {
        var starts = 0
        var wakes = 0
        var catchUps = 0
        var failure: Exception? = null
        suspend fun run(
            paired: Boolean = true,
            userStopped: Boolean = false,
            serviceRunning: Boolean = false,
            mayStartService: Boolean = false,
        ): ListenableWorker.Result = watchdogResult(paired, userStopped, serviceRunning, mayStartService,
            startService = { starts++; startResult },
            wake = { wakes++ },
            catchUp = { catchUps++; failure?.let { throw it } },
        )
    }

    @Test fun runningServiceProbesInsteadOfStartingOrCatchingUp() = runBlocking {
        val calls = Calls()
        assertTrue(calls.run(serviceRunning = true, mayStartService = true) is ListenableWorker.Result.Success)
        assertEquals(1, calls.wakes)
        assertEquals(0, calls.starts)
        assertEquals(0, calls.catchUps)
    }

    @Test fun stoppedOrUnpairedWorkDoesNothing() = runBlocking {
        for ((paired, stopped) in listOf(false to false, true to true)) {
            val calls = Calls()
            assertTrue(calls.run(paired = paired, userStopped = stopped, mayStartService = true)
                is ListenableWorker.Result.Success)
            assertEquals(0, calls.starts)
            assertEquals(0, calls.wakes)
            assertEquals(0, calls.catchUps)
        }
    }

    @Test fun successfulServiceStartDoesNotAlsoCatchUp() = runBlocking {
        val calls = Calls(startResult = true)
        assertTrue(calls.run(mayStartService = true) is ListenableWorker.Result.Success)
        assertEquals(1, calls.starts)
        assertEquals(0, calls.catchUps)
    }

    @Test fun noExemptionOrARefusedStartFetchesOnce() = runBlocking {
        val withoutExemption = Calls()
        assertTrue(withoutExemption.run() is ListenableWorker.Result.Success)
        assertEquals(0, withoutExemption.starts)
        assertEquals(1, withoutExemption.catchUps)
        val refused = Calls(startResult = false)
        assertTrue(refused.run(mayStartService = true) is ListenableWorker.Result.Success)
        assertEquals(1, refused.starts)
        assertEquals(1, refused.catchUps)
    }

    @Test fun failedCatchUpRetries() = runBlocking {
        val calls = Calls().apply { failure = IOException("offline") }
        assertTrue(calls.run() is ListenableWorker.Result.Retry)
        assertEquals(1, calls.catchUps)
    }
}
