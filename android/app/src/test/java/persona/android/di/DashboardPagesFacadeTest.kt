package persona.android.di

import java.io.File
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Dashboards (#15017): `placesChanged` has a default (no events), so the process-owned facade would compile without it
 * and the drawer would never hear `assistant_pages`. The facade must feed it from the selected hub's events.
 */
class DashboardPagesFacadeTest {
    @Test fun productionFacadeForwardsAssistantPagesEvents() {
        val module = generateSequence(File(".").canonicalFile) { it.parentFile }
            .map { File(it, "android/app/src/main/java/persona/android/di/MachineManagementModule.kt") }
            .first { it.isFile }.readText()
        val facade = module.substringAfter("fun extensions(runtime: ConnectionRuntime, application: Application): ExtensionManagement = object : ExtensionManagement {")
            .substringBefore("fun hubUpdates(")
        val forwarding = facade.substringAfter("override val placesChanged: Flow<Unit> =", missingDelimiterValue = "")
            .substringBefore("override ")
        assertTrue("The production facade must override placesChanged", forwarding.isNotEmpty())
        assertTrue("…from the selected hub's events", forwarding.contains("hub?.connection?.events"))
        assertTrue("…keeping only assistant_pages", forwarding.contains("AssistantPagesEvent"))
    }
}
