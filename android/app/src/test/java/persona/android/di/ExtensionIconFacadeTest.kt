package persona.android.di

import java.io.File
import org.junit.Assert.assertTrue
import org.junit.Test

/** The process-owned facade must not silently inherit unsupported optional logo methods. */
class ExtensionIconFacadeTest {
    @Test fun productionFacadeForwardsBothLogoMethodsToItsCurrentConnection() {
        val module = generateSequence(File(".").canonicalFile) { it.parentFile }
            .map { File(it, "android/app/src/main/java/persona/android/di/MachineManagementModule.kt") }
            .first { it.isFile }.readText()
        val facade = module.substringAfter("fun extensions(runtime: ConnectionRuntime, application: Application): ExtensionManagement = object : ExtensionManagement {")
            .substringBefore("fun hubUpdates(")
        assertTrue("The production facade must download through the current connection",
            facade.contains("override suspend fun icon(path: String) = api().icon(path)"))
        assertTrue("Absolute same-hub artwork must be checked against that connection's origin",
            facade.contains("override fun iconPath(value: String) = runCatching { api().iconPath(value) }.getOrNull()"))
    }
}
