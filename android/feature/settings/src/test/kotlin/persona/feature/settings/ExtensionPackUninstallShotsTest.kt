package persona.feature.settings

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.isDialog
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.api.ExtensionFailedMember
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaTheme
import java.io.File

/**
 * Removing a pack (#1154): the dialog with its members offered (off by default, ticked in the shot) and the dialog
 * for a removal that stopped part way, in each style light and dark. Written as PNGs to `build/i18n-shots/`.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h900dp-xhdpi")
class ExtensionPackUninstallShotsTest {
    @get:Rule val rule = createComposeRule()

    private fun out(): File = File(System.getProperty("persona.shots") ?: "build/i18n-shots").also { it.mkdirs() }

    @Test fun drawsTheMembersChoiceAndAPartialUninstall() {
        var look by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        var scene by mutableStateOf("uninstall")
        var run by mutableIntStateOf(0)
        val chosen = mutableListOf<Pair<Boolean, Boolean>>()
        // Members show by name; one not installed keeps its id.
        val names = mapOf("example-notes" to "Example Notes", "example-ui" to "Example UI")
        val partial = PackPartial(listOf("example-notes"), listOf(ExtensionFailedMember("example-ui", "it is still running")), listOf("example-mail"))
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = look) {
                Box(Modifier.fillMaxSize().background(PersonaTheme.palette.background)) {
                    key(run) {
                        when (scene) {
                            "uninstall" -> UninstallDialog("Example Pack", onDismiss = {}, installedVersion = "1.2.0",
                                members = names.named(listOf("example-notes", "example-ui", "example-mail"))) { keep, members -> chosen += keep to members }
                            "partial" -> PackPartialDialog(partial, "Example Pack", names) {}
                        }
                    }
                }
            }
        }
        fun capture(name: String) {
            rule.waitForIdle()
            val bitmap = rule.onNode(isDialog()).captureToImage().asAndroidBitmap()
            File(out(), "extension-pack-$name-${look.name.lowercase()}-${if (dark) "dark" else "light"}.png").outputStream()
                .use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
        }
        // Off by default: Remove alone keeps the members; ticked, they go too.
        rule.waitForIdle()
        rule.onNodeWithText("Remove").performClick()
        rule.onNodeWithTag("uninstall-members").performClick()
        rule.onNodeWithText("Remove").performClick()
        assertEquals(listOf(true to false, true to true), chosen)
        for (s in AppStyle.entries) for (d in listOf(false, true)) {
            look = s; dark = d
            scene = "uninstall"; run++
            rule.waitForIdle()
            rule.onNodeWithTag("uninstall-members").performClick()
            capture("uninstall")
            scene = "partial"; run++
            rule.waitForIdle()
            assertEquals(1, rule.onAllNodesWithText("Pack partly removed").fetchSemanticsNodes().size)
            capture("partial")
        }
    }
}
