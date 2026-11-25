package persona.module.voice

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.graphics.compositeOver
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.icons.PersonaIcons

/** The voice page's own components (moved from :core:designsystem with their checks, #13431). */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h1600dp-xhdpi")
class VoiceComponentsTest {
    @get:Rule val rule = createComposeRule()

    @Test fun hangUpIsRedWordsOnANeutralDiscInEveryStyleAndAppearance() {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        var expected = Color.Unspecified
        rule.setContent {
            PersonaTheme(style = style, darkTheme = dark, reduceMotion = true) {
                val p = PersonaTheme.palette
                expected = p.statusSurface.compositeOver(p.background)
                Column(Modifier.width(400.dp).background(p.background)) {
                    VoiceControl(PersonaIcons.voiceEnd, "End call", {}, Modifier.testTag("voice-danger"), kind = VoiceControlKind.Danger)
                }
            }
        }
        for (s in AppStyle.entries) for (d in listOf(false, true)) {
            style = s; dark = d
            rule.waitForIdle()
            val bitmap = rule.onNodeWithTag("voice-danger").captureToImage().asAndroidBitmap()
            // Inside the disc, clear of the icon. Density is 2px/dp.
            val actual = Color(bitmap.getPixel(bitmap.width / 2, 24))
            for ((a, b) in listOf(actual.red to expected.red, actual.green to expected.green, actual.blue to expected.blue)) {
                assertEquals("voice-danger ${s.key} dark=$d neutral backing", b, a, 1.1f / 255f)
            }
        }
    }
}
