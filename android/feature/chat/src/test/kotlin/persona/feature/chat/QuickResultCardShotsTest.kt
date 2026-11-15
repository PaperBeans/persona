package persona.feature.chat

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import java.io.File
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.Tokens
import persona.core.domain.tasks.QuickResult

/** Every style and appearance draws the result details without a guessed device label (#6864). */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h800dp-xhdpi", application = QuestionTestApplication::class)
class QuickResultCardShotsTest {
    @get:Rule val rule = createComposeRule()

    private fun out(): File = File(System.getProperty("persona.shots") ?: "build/quick-result-card-shots").also { it.mkdirs() }

    @Test fun drawsTheDetailsWithoutADeviceLabelInEveryStyleAndAppearance() {
        val result = QuickResult(reminder = false, callId = "call-1", title = "Dentist",
            time = "2026-11-13T15:30:00Z", place = "Personal")
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                Column(Modifier.testTag("shot").width(400.dp).background(PersonaTheme.palette.background).padding(Tokens.Space.l)) {
                    QuickResultCard(result)
                }
            }
        }
        for (s in AppStyle.entries) for (d in listOf(false, true)) {
            style = s; dark = d
            rule.waitForIdle()
            rule.onAllNodes(hasText("iPhone", substring = true), useUnmergedTree = true).assertCountEquals(0)
            val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
            File(out(), "quick-result-${s.name.lowercase()}-${if (d) "dark" else "light"}.png").outputStream()
                .use { assertTrue(bitmap.compress(Bitmap.CompressFormat.PNG, 100, it)) }
        }
    }
}
