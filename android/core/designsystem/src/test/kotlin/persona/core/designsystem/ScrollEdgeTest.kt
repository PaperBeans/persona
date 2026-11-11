package persona.core.designsystem

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.size
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.assertHeightIsEqualTo
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import dev.chrisbanes.haze.HazeState
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

/**
 * The top overlay stays at full effect strength through its 80 dp of chrome; only the next 28 dp fade into the content.
 * Native Robolectric exercises the actual Liquid and Solid branches in light and dark, isolating the wash and its
 * mask. It does not render the device blur itself. The height assertion also guards the local coordinates supplied to
 * Haze. Native, 1 px per dp.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "zh-w400dp-h900dp-mdpi")
class ScrollEdgeTest {
    @get:Rule val rule = createComposeRule()

    @Test fun topOverlayIsWashedToItsBoundaryThenFadesFor28Dp() {
        rule.setContent {
            Column {
                for (glass in listOf(GlassLevel.Liquid, GlassLevel.Solid)) {
                    for (dark in listOf(false, true)) {
                        PersonaTheme(darkTheme = dark, reduceMotion = true, style = AppStyle.Glass) {
                            CompositionLocalProvider(
                                LocalGlassLevel provides glass,
                                LocalGlassSource provides remember { HazeState() },
                            ) {
                                Box(Modifier.testTag("shot glass=$glass dark=$dark").size(120.dp, 160.dp).background(Content)) {
                                    ScrollEdge(
                                        Modifier.align(Alignment.TopCenter).testTag("edge glass=$glass dark=$dark"),
                                        height = 80.dp,
                                        top = true,
                                        light = Ambience.Calm,
                                    )
                                }
                            }
                        }
                    }
                }
            }
        }
        rule.waitForIdle()

        for (glass in listOf(GlassLevel.Liquid, GlassLevel.Solid)) {
            for (dark in listOf(false, true)) {
                val tag = "glass=$glass dark=$dark"
                rule.onNodeWithTag("edge $tag").assertHeightIsEqualTo(108.dp)
                val bitmap = rule.onNodeWithTag("shot $tag").captureToImage().asAndroidBitmap()
                // The old edge was almost transparent at 79 dp. Full-strength wash must still cover that content boundary.
                val atBoundary = bitmap.getPixel(10, 79)
                val nearFadeEnd = bitmap.getPixel(10, 107)
                if (dark) {
                    assertTrue("$tag: dark wash still covers the overlay boundary", android.graphics.Color.blue(atBoundary) < 160)
                    assertTrue("$tag: the extra 28 dp return to the content colour", android.graphics.Color.blue(nearFadeEnd) > 215)
                } else {
                    assertTrue("$tag: light wash still covers the overlay boundary", android.graphics.Color.red(atBoundary) > 100)
                    assertTrue("$tag: the extra 28 dp return to the content colour", android.graphics.Color.red(nearFadeEnd) < 40)
                }
            }
        }
    }

    private companion object {
        val Content = Color.Blue
    }
}
