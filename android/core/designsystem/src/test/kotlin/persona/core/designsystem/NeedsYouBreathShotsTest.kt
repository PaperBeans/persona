package persona.core.designsystem

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.padding
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import java.io.File

/**
 * The Needs You mark and dot in Glass, light above dark, at moments of one breath (#4209), next to a Working mark that
 * doesn't breathe. Only public components, so the same test draws the marks before and after a change. PNGs in
 * `build/breath-shots/`.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h1600dp-xhdpi")
class NeedsYouBreathShotsTest {
    @get:Rule val rule = createComposeRule()

    private fun out(): File = File(System.getProperty("persona.shots") ?: "build/breath-shots").also { it.mkdirs() }

    @Test fun oneBreath() {
        rule.mainClock.autoAdvance = false
        rule.setContent {
            // Light above dark, both in one picture, so every frame shows both at the same moment of the breath.
            Column(Modifier.testTag("shot")) {
                for (dark in listOf(false, true)) {
                    PersonaTheme(darkTheme = dark, reduceMotion = false, style = AppStyle.Glass) {
                        Row(Modifier.background(PersonaTheme.palette.background).padding(24.dp),
                            horizontalArrangement = Arrangement.spacedBy(24.dp), verticalAlignment = Alignment.CenterVertically) {
                            JStatusMark(Status.NeedsYou)
                            JStatusDot(Status.NeedsYou, Modifier.padding(8.dp))
                            JStatusMark(Status.Working)
                        }
                    }
                }
            }
        }
        var elapsed = 0L
        for (at in listOf(0L, 150L, 400L, 800L)) {
            rule.mainClock.advanceTimeBy(at - elapsed)
            elapsed = at
            val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
            File(out(), "breath-glass-${at}ms.png").outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
        }
    }
}
