package persona.feature.chat

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import coil3.ImageLoader
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import org.junit.Assert.assertNotEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.Item
import persona.core.contracts.SessionKey
import persona.core.contracts.Subagent
import persona.core.contracts.TaskNote
import persona.core.contracts.WorkflowTool
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.Tokens
import persona.core.domain.lines.ChatLine
import persona.core.domain.lines.Subagents
import java.io.File
import java.time.Instant

/**
 * Subagents in an Agent's chat as the task cards of `docs/design/DESIGN_SYSTEM.md` section 4.7 (#5045), in Glass: a running
 * one, folded and opened (its brief, its latest report, its last tool); a completed one opened with its result folded to
 * four lines, a failed one, a stopped background one and an older hub's background one with its output file; a subagent
 * a subagent launched as a flat card of its own. Light and dark; Brief and Board for the diff that shows they did not
 * change. PNGs in `build/subagent-cards-shots/` (or `-Dpersona.shots=<dir>`).
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h2000dp-xhdpi")
class SubagentCardsShotsTest {
    @get:Rule val rule = createComposeRule()
    private var style by mutableStateOf(AppStyle.Glass)
    private var dark by mutableStateOf(false)
    private var scene by mutableStateOf("running")

    private fun out(): File = File(System.getProperty("persona.shots") ?: "build/subagent-cards-shots").also { it.mkdirs() }

    private val base = Instant.parse("2026-11-12T10:00:00Z")
    private fun at(seconds: Long) = base.plusSeconds(seconds).toString()
    private fun user(seq: Int, text: String) = Item("u$seq", seq = seq, role = "user", kind = "text", text = text, ts = at(seq * 5L))
    private fun use(seq: Int, id: String, name: String, vararg input: Pair<String, String>, ts: String = at(seq * 5L)) = Item("c$seq",
        seq = seq, role = "assistant", kind = "tool_use", toolUseId = id, toolName = name, ts = ts,
        toolInput = buildJsonObject { input.forEach { (k, v) -> put(k, v) } })
    private fun agent(seq: Int, id: String, title: String, prompt: String = "Look into $title and report back.", ts: String = at(seq * 5L)) =
        use(seq, id, "Agent", "description" to title, "prompt" to prompt, "subagent_type" to "general-purpose", ts = ts)
    private fun result(seq: Int, id: String, text: String?, error: Boolean = false, task: String? = null, output: String? = null,
        ts: String = at(seq * 5L)) = Item("r$seq", seq = seq, role = "user", kind = "tool_result", toolUseId = id, text = text,
        isError = error, ts = ts, task = task?.let { TaskNote(kind = "agent", status = it, title = "Report", toolUseId = id, outputFile = output) })

    // The running scene happens now, so its work line and "Running for …" read a minute or so.
    private val now = Instant.now().minusSeconds(90)
    private fun recent(seq: Int) = now.plusSeconds(seq * 5L).toString()
    private val runStart = recent(4)
    private val listed = listOf(
        Subagent("a-run", "s1@a-run", "general-purpose", "Run the unit tests", "run", running = true, startedAt = runStart, steps = 12,
            lastTool = WorkflowTool(name = "Bash", summary = "Running the tests"), model = "claude-haiku-4-5-20251001", tokens = 23_400),
        Subagent("a-done", "s1@a-done", "Explore", "Find the crash", "done", startedAt = at(10), steps = 7,
            model = "claude-sonnet-4-5", tokens = 8_200),
        Subagent("a-bad", "s1@a-bad", "general-purpose", toolUseId = "bad", steps = 3, model = "claude-haiku-4-5"),
        Subagent("a-stop", "s1@a-stop", "Explore", "Watch the logs", "stop", background = true, startedAt = at(30), updatedAt = at(95), steps = 2),
        Subagent("a-old", "s1@a-old", "Explore", "Map the old screens", "old", background = true, startedAt = at(45), updatedAt = at(170), steps = 9),
        Subagent("a-lead", "s1@a-lead", "general-purpose", "Plan the migration", "lead", running = true, startedAt = recent(2), steps = 4,
            model = "claude-opus-4-1", tokens = 51_000),
        Subagent("a-kid", "s1@a-kid", "Explore", "List the old tables", null, running = true, startedAt = now.plusSeconds(13).toString(), steps = 6, parentId = "a-lead",
            model = "claude-haiku-4-5"),
    )

    private val longReport = (1..7).joinToString("\n") { "Finding $it: a null session reaches ChatScreen.kt:14$it when the chat reopens." }

    private val scenes = mapOf(
        "running" to listOf(
            user(1, "Check the release before tagging it").copy(ts = recent(1)),
            use(2, "read", "Read", "file_path" to "/repo/CHANGELOG.md", ts = recent(2)), result(3, "read", "ok", ts = recent(3)),
            agent(4, "run", "Run the unit tests", "Run the unit tests in core/domain and core/data, then report each failure with its cause.", ts = runStart),
            result(5, "run", "Async agent launched successfully.\nagentId: a-run", task = "running", ts = recent(5)),
            result(6, "run", "Half way: core/domain passed, core/data is running.", task = "running", ts = recent(6)),
        ),
        "done" to listOf(
            user(1, "Look at the crash and the old screens"),
            agent(2, "done", "Find the crash"), result(20, "done", "$longReport\nagentId: a-done"),
            agent(3, "bad", "Reproduce on a tablet"), result(4, "bad", "No tablet emulator is installed.", error = true),
            use(6, "stop", "Agent", "description" to "Watch the logs", "prompt" to "Tail the logs", "run_in_background" to "true"),
            result(7, "stop", "Async agent launched successfully.\nagentId: a-stop", task = "running"),
            result(19, "stop", null, task = "killed"),
            // An older hub: the receipt is the result, its notice says how it went and where it wrote.
            use(9, "old", "Agent", "description" to "Map the old screens", "prompt" to "List the screens", "run_in_background" to "true"),
            result(10, "old", "Async agent launched successfully.\nagentId: a-old"),
            Item("n34", seq = 34, kind = "notice", text = "done", ts = at(170), task = TaskNote(kind = "agent", status = "completed",
                title = "Map the old screens", toolUseId = "old", detail = "Twelve screens, three still on the old theme.", outputFile = "/tmp/screens.md")),
        ),
        "nested" to listOf(
            user(1, "Plan the database migration").copy(ts = recent(1)),
            agent(2, "lead", "Plan the migration", ts = recent(2)),
            use(3, "grep", "Grep", "pattern" to "CREATE TABLE", ts = recent(3)), result(4, "grep", "12 matches", ts = recent(4)),
        ),
    )

    @Test fun subagentsAreTaskCards() {
        val session = SessionKey("box", "s1")
        rule.mainClock.autoAdvance = false // spinners never settle
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                val context = LocalContext.current
                val loader = remember { ImageLoader.Builder(context).build() }
                DisposableEffect(loader) { onDispose { loader.shutdown() } }
                val timeline = remember(scene) { ChatTimeline() }
                val folds = remember(scene) { SubagentFolds() }
                val snapshot = timeline.lines(scenes.getValue(scene), live = scene != "done", open = setOf("w-u1"))
                // As the chat page: nested subagents are cards of their own in Glass only.
                val lines = if (style == AppStyle.Glass) Subagents.withNested(snapshot.lines, listed) else snapshot.lines
                val groups = lines.filterIsInstance<ChatLine.Work>().associateBy { it.id }
                val last = lines.filterIsInstance<ChatLine.Step>().associate { it.groupId to it.step.id }
                val links = ChatSubagents(listed.filter { it.toolUseId != null }.associate { it.toolUseId!! to SessionKey("box", it.sessionId) },
                    {}, listed.associateBy(Subagents::callOf))
                CompositionLocalProvider(LocalChatTaskIndex provides snapshot.tasks, LocalChatSubagents provides links,
                    LocalSubagentFolds provides folds) {
                    // Each scene starts as the reader would find it: nothing opened in another scene carries over.
                    Column(Modifier.testTag("shot").width(400.dp).background(PersonaTheme.palette.background).padding(Tokens.Space.l)) { key(scene) {
                        lines.forEach { line ->
                            ChatLineContent(line, session, loader, null, onToggle = {}, onFold = {}, folding = emptySet(),
                                foldErrors = emptyMap(), onFullText = {},
                                workRunning = (line as? ChatLine.Step)?.let { groups[it.groupId]?.live } == true,
                                lastWorkStep = (line as? ChatLine.Step)?.let { last[it.groupId] == it.step.id } == true)
                        }
                    } }
                }
            }
        }
        frame()
        // Running: Subagent, its title, Running; then what ran it, the model, tokens, tool uses and how long it has run.
        rule.onNodeWithText("Run the unit tests").assertIsDisplayed()
        rule.onNodeWithText("Running").assertIsDisplayed()
        rule.onNodeWithText("Haiku 4.5").assertIsDisplayed()
        rule.onNodeWithText("23.4k tokens").assertIsDisplayed()
        rule.onNodeWithText("12 tool uses").assertIsDisplayed()
        rule.onNodeWithText("Running for", substring = true).assertIsDisplayed()
        rule.onNodeWithText("Half way", substring = true).assertDoesNotExist()
        rule.onNodeWithText("View Subagent").assertDoesNotExist()
        capture("running")
        style = AppStyle.Glass; dark = false
        frame()
        rule.onNodeWithText("Run the unit tests").performClick()
        frame()
        rule.onNodeWithText("Run the unit tests in core/domain", substring = true).assertIsDisplayed()
        rule.onNodeWithText("Half way: core/domain passed, core/data is running.").assertIsDisplayed()
        rule.onNodeWithText("Last tool: Bash").assertIsDisplayed()
        capture("running-open", glassOnly = true)

        // Done: every subagent is still its card, with how it went and how long it took.
        scene = "done"; style = AppStyle.Glass; dark = false
        frame()
        rule.onAllNodesWithTag("chat.subagent-card", useUnmergedTree = true).assertCountEquals(4)
        rule.onAllNodesWithText("Completed").assertCountEquals(2)
        rule.onNodeWithText("Failed").assertIsDisplayed()
        rule.onNodeWithText("Stopped").assertIsDisplayed()
        rule.onNodeWithText("1m 30s").assertIsDisplayed()
        capture("done")
        style = AppStyle.Glass; dark = false
        frame()
        rule.onNodeWithText("Find the crash").performClick()
        rule.onNodeWithText("Map the old screens").performClick()
        frame()
        rule.onNodeWithText("Finding 1", substring = true).assertIsDisplayed()
        rule.onNodeWithText("Show full result").assertIsDisplayed()
        rule.onNodeWithText("Twelve screens, three still on the old theme.").assertIsDisplayed()
        rule.onNodeWithText("Output: /tmp/screens.md").assertIsDisplayed()
        capture("done-open", glassOnly = true)
        rule.onNodeWithText("Show full result").performClick()
        frame()
        rule.onNodeWithText("Collapse result").assertIsDisplayed()

        // A subagent's own subagent: a flat card after the step that started before it.
        scene = "nested"; style = AppStyle.Glass; dark = false
        frame()
        rule.onNodeWithText("List the old tables").assertIsDisplayed()
        rule.onAllNodesWithTag("chat.subagent-card", useUnmergedTree = true).assertCountEquals(2)
        capture("nested", glassOnly = true)
    }

    private fun frame() {
        Snapshot.sendApplyNotifications()
        rule.mainClock.advanceTimeBy(1_000) // a card opening settles
        repeat(3) { rule.mainClock.advanceTimeByFrame() }
    }

    /** Glass light and dark, and (unless [glassOnly]) Brief and Board to show they are as they were. */
    private fun capture(name: String, glassOnly: Boolean = false) {
        val styles = if (glassOnly) listOf(AppStyle.Glass) else AppStyle.entries
        for (s in styles) {
            val corners = mutableListOf<Int>()
            for (d in listOf(false, true)) {
                style = s; dark = d
                frame()
                val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
                corners += bitmap.getPixel(2, 2)
                File(out(), "subagents-$name-${s.name.lowercase()}-${if (d) "dark" else "light"}.png").outputStream()
                    .use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
            }
            assertNotEquals("$s dark capture must differ from light", corners[0], corners[1])
        }
    }
}
