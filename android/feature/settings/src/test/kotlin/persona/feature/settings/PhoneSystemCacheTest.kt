package persona.feature.settings

import org.junit.Assert.assertEquals
import org.junit.Test

/** The phone's system is probed once per system build, not on every process start (#4041). */
class PhoneSystemCacheTest {
    private var stored: String? = null
    private var probes = 0
    private var probed = SystemKind("ColorOS", "V16.0", "ro.build.version.oplusrom")

    private fun system(fingerprint: String) =
        cachedPhoneSystem(stored, fingerprint, { probes++; probed }) { stored = it }

    @Test fun probedOncePerBuildAndAgainAfterAnUpdate() {
        assertEquals(probed, system("vendor/device/1:16/A.1/1:user/release-keys"))
        assertEquals(1, probes)
        // Every later process start of the same build reads it back.
        repeat(3) { assertEquals(probed, system("vendor/device/1:16/A.1/1:user/release-keys")) }
        assertEquals(1, probes)

        // A system update changes the fingerprint, and maybe the answer.
        probed = SystemKind("ColorOS", "V16.1", "ro.build.version.oplusrom")
        assertEquals(probed, system("vendor/device/1:16/A.2/2:user/release-keys"))
        assertEquals(2, probes)
        assertEquals(probed, system("vendor/device/1:16/A.2/2:user/release-keys"))
        assertEquals(2, probes)
    }

    @Test fun plainAndroidIsKeptToo() {
        // No vendor property answers: every probe runs, so this is the case worth keeping most.
        probed = SystemKind("Android", "16", "")
        repeat(2) { assertEquals(probed, system("google/phone/1:16/B.1/1:user/release-keys")) }
        assertEquals(1, probes)
    }

    @Test fun anUnreadableOrUnkeyableValueIsProbedAgain() {
        for (bad in listOf("", "garbage", "google/phone/1:16/B.1/1:user/release-keys\n\n16\n", "a\nb\nc\nd\ne")) {
            stored = bad
            probes = 0
            assertEquals(probed, system("google/phone/1:16/B.1/1:user/release-keys"))
            assertEquals(bad, 1, probes)
        }
        // Without a fingerprint nothing can say the stored value is still right: probe every time, store nothing.
        stored = null
        probes = 0
        repeat(2) { system("") }
        assertEquals(2, probes)
        assertEquals(null, stored)
        // A version with a line break would not read back: it is not stored.
        probed = SystemKind("ColorOS", "V16\n.0", "ro.build.version.oplusrom")
        system("vendor/device/1:16/A.1/1:user/release-keys")
        assertEquals(null, stored)
    }
}
