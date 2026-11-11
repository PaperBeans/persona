package persona.feature.settings

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.api.ExtensionOpMachines
import persona.core.contracts.api.ExtensionOpStep

/** The sync step's "done of total computers" is worded in the English plural form of the total (#4081). */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en")
class ExtensionOperationMachinesTest {
    private val res get() = ApplicationProvider.getApplicationContext<Context>().resources

    private fun detail(done: Int, total: Int) = OperationText.detail(res, ExtensionOpStep("nodes", "running", machines = ExtensionOpMachines(done, total)))

    @Test fun singularAndPlural() {
        assertEquals("0 / 1 computer", detail(0, 1))
        assertEquals("1 / 1 computer", detail(1, 1))
        assertEquals("1 / 2 computers", detail(1, 2))
    }
}
