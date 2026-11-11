package persona.feature.settings

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config

/** The background diagnostics exit record count is worded in the English plural form (#3906). */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en")
class BackgroundHistoryCountTest {
    private val res get() = ApplicationProvider.getApplicationContext<Context>().resources

    @Test fun singularAndPlural() {
        assertEquals("1 system record", res.getQuantityString(R.plurals.settings_background_history_count, 1, 1))
        assertEquals("3 system records", res.getQuantityString(R.plurals.settings_background_history_count, 3, 3))
    }
}
