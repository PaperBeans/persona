package persona.feature.agents

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config

/** The paged Agents list's loaded line follows the total in the English plural form (#4078). */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en")
class AgentsPageLoadedTest {
    private val res get() = ApplicationProvider.getApplicationContext<Context>().resources

    @Test fun nounFollowsTheTotal() {
        assertEquals("1 of 1 session loaded", res.getQuantityString(R.plurals.agents_page_loaded, 1, 1, 1))
        assertEquals("1 of 2 sessions loaded", res.getQuantityString(R.plurals.agents_page_loaded, 2, 1, 2))
        assertEquals("0 of 0 sessions loaded", res.getQuantityString(R.plurals.agents_page_loaded, 0, 0, 0))
    }
}
