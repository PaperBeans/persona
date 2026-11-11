package persona.feature.settings

import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.async
import kotlinx.coroutines.runBlocking
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.api.*

/** Settings › Devices › Connect a new device: the code, its countdown, and the device that took it. */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
class ConnectDeviceModelTest {
    private class Hub : DeviceSettings {
        var list = listOf(PairedDevice("me", "这台手机", "phone", "full"))
        var codes = 0
        var fail: Exception? = null
        /** When set, the next devices() read waits for it, then answers with the list as it is by then. */
        var hold: CompletableDeferred<Unit>? = null
        override suspend fun devices(): PairedDevices {
            hold?.let { hold = null; it.await() }
            return PairedDevices(list, "me")
        }
        override suspend fun revoke(credentialId: String) {}
        override suspend fun preferences() = DeviceNotificationPreferences()
        override suspend fun savePreferences(preferences: DeviceNotificationPreferences) = preferences
        override suspend fun pairCode(): AppPairCode {
            fail?.let { throw it }
            codes++
            return AppPairCode("jp_$codes", 600_000, "persona://pair?token=jp_$codes", QrImage(1, listOf("1")))
        }
    }

    @Test fun theCodeCountsDownAndTheNewDeviceIsNamed() = runBlocking {
        val hub = Hub()
        var now = 0L
        val model = ConnectDeviceModel(hub) { now }
        model.make()
        assertEquals("persona://pair?token=jp_1", model.state.value.code?.pairUrl)
        assertEquals(600, model.state.value.secondsLeft)
        assertEquals("10:00", clock(model.state.value.secondsLeft))
        assertFalse(model.poll())
        hub.list = hub.list + PairedDevice("new", "", "phone", "full")
        assertTrue(model.poll())
        assertEquals(ConnectPhase.Paired("new"), model.state.value.phase)

        // Another one: a new code; it expires.
        model.make()
        assertEquals(2, hub.codes)
        now = 600_000
        model.tick()
        assertTrue(model.state.value.expired)
        hub.list = hub.list + PairedDevice("late", "迟到的", "phone", "full")
        assertFalse("an expired code waits for nothing", model.poll())
    }

    @Test fun aDeviceThatPairedWhileThePageWasStoppedIsShownOnReturn() = runBlocking {
        val hub = Hub()
        var now = 0L
        var paired = 0
        val model = ConnectDeviceModel(hub) { now }
        assertFalse("no code yet: nothing to catch up on", model.catchUp { paired++ })
        model.make()
        assertFalse("nobody took the code", model.catchUp { paired++ })
        assertEquals(0, paired)
        // The other device scanned the code, then the screen went off; the code expired before the page came back.
        hub.list = hub.list + PairedDevice("new", "新手机", "phone", "full")
        now = 700_000
        model.tick()
        assertTrue(model.state.value.expired)
        assertFalse("polling still waits for nothing", model.poll())
        assertTrue(model.catchUp { paired++ })
        assertEquals(ConnectPhase.Paired("新手机"), model.state.value.phase)
        assertEquals(1, paired)
        assertFalse("once paired, nothing more to catch up on", model.catchUp { paired++ })
        assertEquals(1, paired)
    }

    @Test fun aNewCodeAskedForDuringTheCatchUpReadIsNotMarkedPaired() = runBlocking {
        val hub = Hub()
        var paired = 0
        val model = ConnectDeviceModel(hub) { 0 }
        model.make()
        val read = CompletableDeferred<Unit>()
        hub.hold = read
        // Back in front: the catch-up's read of the device list is on its way...
        val caught = async(start = CoroutineStart.UNDISPATCHED) { model.catchUp { paired++ } }
        assertFalse(caught.isCompleted)
        // ...when the user taps "New code".
        model.make()
        val fresh = model.state.value.phase
        assertEquals("jp_2", (fresh as ConnectPhase.Showing).code.token)
        // The late answer has a device the old code's list did not have.
        hub.list = hub.list + PairedDevice("new", "新手机", "phone", "full")
        read.complete(Unit)
        assertFalse("the answer was for the old code", caught.await())
        assertSame(fresh, model.state.value.phase)
        assertEquals(0, paired)
        // The new code still finds it on its own next read.
        assertTrue(model.poll())
        assertEquals(ConnectPhase.Paired("新手机"), model.state.value.phase)
    }

    @Test fun aRefusalIsShownWithItsReason() = runBlocking {
        val hub = Hub().apply { fail = IllegalStateException("x") }
        val model = ConnectDeviceModel(hub) { 0 }
        model.make()
        assertTrue(model.state.value.phase is ConnectPhase.Failed)
        assertEquals(0, secondsLeft(0, 5))
        assertEquals("0:59", clock(59))
    }

    @Test fun anUnreadableExpiryStillShowsTheCode() {
        assertEquals(1_000 + PairCodeLifeMs, localExpiry(0, 1_000))
        assertEquals(1_000 + PairCodeLifeMs, localExpiry(500, 1_000))
        val state = ConnectDeviceState(ConnectPhase.Showing(AppPairCode("jp", localExpiry(0, 1_000), "persona://pair")), now = 1_000)
        assertFalse(state.expired)
        assertEquals(600, state.secondsLeft)
    }
}
