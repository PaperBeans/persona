package persona.feature.settings

import androidx.activity.ComponentActivity
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.designsystem.AppStyle
import persona.core.designsystem.AppStyleStore
import persona.core.designsystem.GroupPosition
import persona.core.designsystem.LayoutDensity
import persona.core.designsystem.LocalAppStyleStore
import persona.core.designsystem.MessageItem
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.Status
import persona.core.designsystem.Tokens
import persona.core.designsystem.pagePadding
import java.io.File

/**
 * Settings › Appearance › Layout density (#5564): tapping "Compact" makes the page itself tighter (a shorter page, the same
 * text), and "Standard" brings it back. PNGs for every style, light and dark, both densities, in `build/density-shots/`.
 */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en-w400dp-h3200dp-xhdpi")
@org.robolectric.annotation.GraphicsMode(org.robolectric.annotation.GraphicsMode.Mode.NATIVE)
class LayoutDensityShotsTest {
    @get:Rule val rule = createAndroidComposeRule<ComponentActivity>()

    @Test fun compactTightensTheAppearancePage() {
        var dark by mutableStateOf(false)
        var style by mutableStateOf(AppStyle.Glass)
        lateinit var store: AppStyleStore
        rule.setContent {
            val context = LocalContext.current
            store = remember { AppStyleStore(context) }
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                CompositionLocalProvider(LocalAppStyleStore provides store) {
                    Box(Modifier.testTag("shot").width(400.dp).background(PersonaTheme.palette.background)) {
                        Column {
                            AppearanceSettings(Modifier.fillMaxWidth().padding(horizontal = pagePadding))
                            // A slice of the inbox, so the shots show a list too, not only this page.
                            Column(Modifier.padding(horizontal = pagePadding, vertical = Tokens.Space.l)) {
                                MessageItem("Design team", "Design review moved to Wednesday", onClick = {}, time = "10:42",
                                    preview = "Can we push the density review to Wed afternoon?", unread = 2, position = GroupPosition.of(0, 3))
                                MessageItem("Build bot", "android: compact density", onClick = {}, time = "09:15", status = Status.Working,
                                    preview = "Running unit tests", tags = listOf("CI"), position = GroupPosition.of(1, 3))
                                MessageItem("Sam Lee", "Lunch?", onClick = {}, time = "Yesterday", preview = "Noodles at noon",
                                    position = GroupPosition.of(2, 3))
                            }
                        }
                    }
                }
            }
        }
        val out = File(System.getProperty("persona.shots") ?: "build/density-shots").also { it.mkdirs() }
        val heights = mutableMapOf<LayoutDensity, Int>()
        for (density in LayoutDensity.entries) {
            rule.onNodeWithText(density.labelFor()).performClick()
            rule.waitForIdle()
            assertEquals(density, store.density.value)
            for (s in AppStyle.entries) for (d in listOf(false, true)) {
                rule.runOnIdle { store.setStyle(s); style = s; dark = d }
                rule.waitForIdle()
                val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
                if (s == AppStyle.Brief && !d) heights[density] = bitmap.height
                out.resolve("density-${density.key}-${s.name.lowercase()}-${if (d) "dark" else "light"}.png").outputStream().use {
                    bitmap.compress(android.graphics.Bitmap.CompressFormat.PNG, 100, it)
                }
            }
            rule.runOnIdle { store.setStyle(AppStyle.Glass); style = AppStyle.Glass; dark = false }
            rule.waitForIdle()
        }
        assertTrue("Compact must be shorter: $heights", heights.getValue(LayoutDensity.Compact) < heights.getValue(LayoutDensity.Standard))
        rule.onNodeWithText("Standard").performClick()
        rule.waitForIdle()
        assertEquals(LayoutDensity.Standard, store.density.value)
    }

    private fun LayoutDensity.labelFor() = when (this) { LayoutDensity.Standard -> "Standard"; LayoutDensity.Compact -> "Compact" }
}
