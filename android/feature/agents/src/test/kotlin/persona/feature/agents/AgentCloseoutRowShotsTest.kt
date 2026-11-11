package persona.feature.agents

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
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithContentDescription
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.Closeout
import persona.core.contracts.CloseoutChecked
import persona.core.contracts.Session
import persona.core.contracts.WorkState
import persona.core.designsystem.*
import persona.core.domain.sort.AgentSession
import java.io.File
import java.time.Instant

/**
 * A stopped agent's row once it ended its turn with a closeout (#3612): today's line, and under it where the closeout
 * stands on merged and deployed; a claim the hub found untrue is in the caution color, in words, and marked beside the
 * title; a turn that is not finished has its own mark; a long line is cut, never wrapped. Every style, light and dark;
 * PNGs in `build/closeout-row-shots/`.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h700dp-xhdpi")
class AgentCloseoutRowShotsTest {
    @get:Rule val rule = createComposeRule()

    private fun out(): File = File(System.getProperty("persona.shots") ?: "build/closeout-row-shots").also { it.mkdirs() }

    private val now = Instant.parse("2026-11-10T10:14:30Z")
    private fun row(id: String, title: String, work: WorkState?, words: String = "Done.") =
        AgentSession(Session(machineId = "server", sessionId = id, title = title, cwd = "/home/me/code/app", status = "done", state = "unread",
            lastText = words, updatedAt = "2026-11-10T10:12:00Z", work = work), stateSince = "2026-11-10T10:00:00Z")

    private val rows = listOf(
        row("confirmed", "Fix the importer retry", WorkState("done", line = "Retry the import once when the hub restarts",
            closeout = Closeout("done", merged = "yes", deployed = "no", checked = CloseoutChecked(merged = "yes")))),
        row("contradicted", "Ship the settings page", WorkState("", line = "Says merged, but the pull request is not merged",
            closeout = Closeout("done", merged = "yes", checked = CloseoutChecked(merged = "no")))),
        row("unfinished", "Migrate the billing tables", WorkState("", line = "Not finished: the migration script for the invoices table is still missing",
            closeout = Closeout("not_done", missing = "the migration script", merged = "no", deployed = "no"))),
        row("plain", "Tidy the README", WorkState("done", line = "Rewrote the setup section")),
        row("untriaged", "Explore the crash logs", null, words = "Nothing found in the last week of logs."),
    )

    @Test fun drawsWhereTheCloseoutStandsInEveryStyleAndAppearance() {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        rule.setContent {
            PersonaTheme(style = style, darkTheme = dark, reduceMotion = true) {
                Column(Modifier.testTag("shot").width(400.dp).background(PersonaTheme.palette.background).padding(Tokens.Space.l)) {
                    rows.forEachIndexed { index, row ->
                        SlotRow(row, "Workstation", AgentRowMetadata(), now, false, false, false, depth = 0,
                            position = GroupPosition(index == 0, index == rows.lastIndex), located = false, onOpen = {}, menuItems = { emptyList() })
                    }
                }
            }
        }
        for (nextStyle in AppStyle.entries) for (nextDark in listOf(false, true)) {
            rule.runOnIdle { style = nextStyle; dark = nextDark }
            rule.onNodeWithText("✓ Retry the import once when the hub restarts", substring = true).assertIsDisplayed()
            // where the closeout stands: its own line of facts, never the cut end of the line above
            rule.onNodeWithText("merged ✓   not deployed", substring = true).assertIsDisplayed()
            rule.onNodeWithText("✗ says merged, isn't", substring = true).assertIsDisplayed()
            rule.onNodeWithText("not merged   not deployed", substring = true).assertIsDisplayed()
            rule.onNodeWithContentDescription("The hub found its closeout claim untrue", useUnmergedTree = true).assertIsDisplayed()
            rule.onNodeWithContentDescription("Not finished", useUnmergedTree = true).assertIsDisplayed()
            // rows without a closeout are as before: no mark, today's line
            assertEquals(1, rule.onAllNodesWithContentDescription("Not finished", useUnmergedTree = true).fetchSemanticsNodes().size)
            rule.onNodeWithText("✓ Rewrote the setup section", substring = true).assertIsDisplayed()
            rule.onNodeWithText("Nothing found in the last week of logs.", substring = true).assertIsDisplayed()
            val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
            val name = "closeout-row-${nextStyle.name.lowercase()}-${if (nextDark) "dark" else "light"}"
            File(out(), "$name.png").outputStream().use { assertTrue(bitmap.compress(Bitmap.CompressFormat.PNG, 100, it)) }
        }
    }
}
