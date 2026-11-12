package persona.feature.chat

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.getBoundsInRoot
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import coil3.ImageLoader
import kotlinx.serialization.json.Json
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.Item
import persona.core.contracts.SessionKey
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.Tokens
import persona.core.domain.lines.ChatLine
import persona.core.domain.lines.ToolCall
import persona.core.domain.lines.WorkItem
import java.io.File

/**
 * An opened step of a tool no table knows (#4339 C1, iOS `ToolArgumentsDetail`) in the middle of a run: its subject on
 * the row, and on Glass its input as `key: value` lines in words, right of the rail (#5025); Brief and Board keep the
 * input as code. Every style, light and dark. PNGs in `build/tool-arguments-shots/` (or `-Dpersona.shots=<dir>`).
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h1000dp-xhdpi")
class ToolArgumentsShotsTest {
    @get:Rule val rule = createComposeRule()

    private fun out(): File = File(System.getProperty("persona.shots") ?: "build/tool-arguments-shots").also { it.mkdirs() }

    private fun step(id: String, name: String, input: String, output: String) = WorkItem.Tool(ToolCall(
        Item(id, kind = "tool_use", role = "assistant", toolName = name, toolInput = Json.parseToJsonElement(input), toolUseId = id).asLineItem(),
        Item("$id-r", kind = "tool_result", role = "user", text = output, toolUseId = id).asLineItem(), emptyList()))

    private val issue = step("t1", "mcp__github__create_issue", """{"owner":"example","repo":"weather","title":"CSV export times out on a year of hourly data",
        "body":"Exporting a year takes 41 s and then fails.\n\nSteps: Settings > Export > CSV.\nExpected: the file in under 5 s.",
        "labels":["bug","export"],"draft":false,"milestone":12,"meta":{"source":"crash-report","app":"weather/2.4"}}""",
        "Created issue #42")

    // The opened step sits in the middle of a run: the rail above and below it runs past its details, never through them.
    private val run = listOf(
        step("t0", "Read", """{"file_path":"/w/weather/Export.kt"}""", "1\tfun export() {}"),
        issue,
        step("t2", "Bash", """{"command":"./gradlew :export:test"}""", "BUILD SUCCESSFUL"),
    )

    @Test fun anUnknownToolListsItsArgumentsOnGlass() {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                val context = LocalContext.current
                val loader = remember { ImageLoader.Builder(context).build() }
                DisposableEffect(loader) { onDispose { loader.shutdown() } }
                Column(Modifier.testTag("shot").width(400.dp).background(PersonaTheme.palette.background).padding(Tokens.Space.l)) {
                    run.forEachIndexed { i, step ->
                        ChatLineContent(ChatLine.Step("w", step, i == 0), SessionKey("mac", "s1"), loader, recordedDuration = null,
                            onToggle = {}, onFold = {}, folding = emptySet(), foldErrors = emptyMap(), onFullText = {},
                            lastWorkStep = i == run.lastIndex)
                    }
                }
            }
        }
        // The row names the tool in words and says what the call is about under it.
        rule.onNodeWithText("CSV export times out on a year of hourly data", useUnmergedTree = true).assertIsDisplayed()
        rule.onNodeWithText("GitHub · Create issue", useUnmergedTree = true).assertIsDisplayed().performClick()
        rule.waitForIdle()
        val corners = mutableMapOf<AppStyle, MutableList<Int>>()
        for (s in AppStyle.entries) for (d in listOf(false, true)) {
            style = s; dark = d
            Snapshot.sendApplyNotifications()
            rule.waitForIdle()
            val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
            corners.getOrPut(s) { mutableListOf() } += bitmap.getPixel(2, 2)
            File(out(), "tool-arguments-${s.name.lowercase()}-${if (d) "dark" else "light"}.png").outputStream()
                .use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
        }
        corners.values.forEach { assertNotEquals("dark capture must differ", it[0], it[1]) }
        // Glass lists the arguments in words; the others keep the input as code.
        style = AppStyle.Glass
        Snapshot.sendApplyNotifications()
        rule.waitForIdle()
        rule.onAllNodesWithTag("tool-arguments", useUnmergedTree = true).assertCountEquals(1)
        // Right of the rail, which runs through the middle of the icons (#5025): the details start past the icon.
        val left = rule.onNodeWithTag("tool-arguments", useUnmergedTree = true).getBoundsInRoot().left
        assertTrue("details at $left cross the rail", left >= Tokens.Space.l + Tokens.Space.m + Tokens.Size.iconXS)
        rule.onNodeWithText("Milestone", useUnmergedTree = true).assertIsDisplayed()
        rule.onNodeWithText("bug, export", useUnmergedTree = true).assertIsDisplayed()
        style = AppStyle.Brief
        Snapshot.sendApplyNotifications()
        rule.waitForIdle()
        rule.onAllNodesWithTag("tool-arguments", useUnmergedTree = true).assertCountEquals(0)
    }
}
