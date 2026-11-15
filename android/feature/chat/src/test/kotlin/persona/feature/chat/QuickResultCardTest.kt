package persona.feature.chat

import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.test.ext.junit.runners.AndroidJUnit4
import java.time.Instant
import java.util.Locale
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.designsystem.LocalizedFormats
import persona.core.designsystem.PersonaTheme
import persona.core.domain.tasks.QuickResult

/** A quick result shows what was added, as on iOS, without inventing a source device (#6864). */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en")
class QuickResultCardTest {
    @get:Rule val rule = createComposeRule()

    @Test fun showsResultDetailsWithoutInventingADeviceSource() {
        val result = QuickResult(reminder = false, callId = "call-1", title = "Dentist",
            time = "2026-11-13T15:30:00Z", place = "Personal")
        val time = LocalizedFormats.format(Instant.parse(result.time!!), LocalizedFormats.MONTH_DAY_TIME, Locale.ENGLISH)
        rule.setContent { PersonaTheme(reduceMotion = true) { QuickResultCard(result) } }

        rule.onNodeWithText("Added to calendar").assertExists()
        rule.onNodeWithText("$time · ${result.title} · ${result.place}").assertExists()
        rule.onAllNodes(hasText("iPhone", substring = true), useUnmergedTree = true).assertCountEquals(0)
    }
}
