package persona.feature.chat

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.Session
import persona.core.designsystem.*
import java.io.File

/**
 * The chat's strip of its space's agents with a herdr tab of three (#2320): the tab's agents side by side in pane order,
 * the launcher marked, then a tab of one. Every style, light and dark; PNGs in `build/sibling-shots/`.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "zh-w400dp-h800dp-xhdpi")
class SiblingTabsShotsTest {
    @get:Rule val rule = createComposeRule()

    private fun out(): File = File(System.getProperty("persona.shots") ?: "build/sibling-shots").also { it.mkdirs() }

    private fun pane(id: String, slot: Int, place: Int?, state: String, agent: String = "claude", name: String = "$slot") =
        Session(machineId = "server", sessionId = id, title = id, spaceId = "w1", slotId = "t$slot", slotIndex = slot, slotName = name,
            paneId = "w1:p$id", paneIndex = place, agent = agent, state = state, status = if (state == "idle") "idle" else "working")

    @Test fun drawsTheStripInEveryStyleAndAppearance() {
        val lead = pane("W", 1, 0, "working")
        val all = listOf(lead, pane("2E", 1, 1, "needs_you", agent = "codex"), pane("31", 1, 2, "unread"), pane("3A", 2, 0, "working", name = "deps"))
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        rule.setContent {
            PersonaTheme(style = style, darkTheme = dark, reduceMotion = true) {
                Column(Modifier.testTag("shot").width(400.dp).background(PersonaTheme.palette.background)) {
                    ChatSiblingTabs(lead, spaceTabs(lead, all), onOpenSession = {})
                }
            }
        }
        for (nextStyle in AppStyle.entries) for (nextDark in listOf(false, true)) {
            rule.runOnIdle { style = nextStyle; dark = nextDark }
            rule.onNodeWithText("#1 Claude 1 发起").assertIsDisplayed()
            val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
            File(out(), "chat-tabs-${nextStyle.name.lowercase()}-${if (nextDark) "dark" else "light"}.png").outputStream().use {
                assertTrue(bitmap.compress(Bitmap.CompressFormat.PNG, 100, it))
            }
        }
    }
}
