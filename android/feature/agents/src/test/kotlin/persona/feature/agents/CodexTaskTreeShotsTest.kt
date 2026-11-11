package persona.feature.agents

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.Session
import persona.core.contracts.SessionCapabilities
import persona.core.designsystem.*
import persona.core.domain.sort.*
import java.io.File
import java.time.Instant

@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h900dp-xhdpi")
class CodexTaskTreeShotsTest {
    @get:Rule val rule = createComposeRule()

    private var expanded by mutableStateOf(emptySet<String>())
    private var style by mutableStateOf(AppStyle.Glass)
    private var dark by mutableStateOf(false)
    private var frame by mutableStateOf(0)

    /** The tree's lines for [list] (one computer, so no computer heading), drawn as the Agents page draws them. */
    private fun show(list: AgentListResult) = rule.setContent {
        PersonaTheme(style = style, darkTheme = dark, reduceMotion = true) {
            // Drawn afresh for each capture: a tap's ripple never finishes under Robolectric and would stay on the header.
            Column(Modifier.testTag("shot").width(400.dp).background(PersonaTheme.palette.background)) { key(frame) {
                agentTreeEntries(list, "machine", expandedTasks = expanded).forEach { entry ->
                    when (entry) {
                        is TreeRowEntry -> SlotRow(entry.row, null, AgentRowMetadata(), Instant.EPOCH, false, false, false,
                            depth = entry.depth, position = GroupPosition(entry.first, entry.last), located = entry.located, onOpen = {}, menuItems = { emptyList() }, branch = entry.branch)
                        is TreeChildrenEntry -> AgentSpaceHeader(stringResource(R.string.agents_child_tasks, entry.count), !entry.expanded,
                            { expanded = if (entry.expanded) expanded - entry.parent.id else expanded + entry.parent.id },
                            Modifier.testTag(entry.key), depth = entry.depth, kind = stringResource(R.string.agents_child_tasks_kind))
                        is TreeGroupEntry -> AgentSpaceHeader(entry.group.title.text(), false, {}, depth = entry.depth)
                        else -> Unit
                    }
                }
            } }
        }
    }

    private fun capture(folder: String, name: String) {
        rule.runOnIdle { frame++ }
        val out = File("build/$folder").also { it.mkdirs() }
        val image = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
        File(out, "$name.png").outputStream().use { assertTrue(image.compress(Bitmap.CompressFormat.PNG, 100, it)) }
    }

    @Test fun expandsAndCollapsesRealTaskRowsInEveryStyleAndTheme() {
        fun task(id: String, parent: String? = null) = AgentSession(Session(machineId = "machine", sessionId = id,
            title = id, cwd = "/code/app", sourceKind = "codex_history", sourceThreadId = id, parentThreadId = parent, state = "working"))
        show(buildAgentList(listOf(task("Main task"), task("Review", "Main task"), task("Regression", "Review"))))
        for (nextStyle in AppStyle.entries) for (nextDark in listOf(false, true)) {
            rule.runOnIdle { style = nextStyle; dark = nextDark; expanded = emptySet() }
            rule.onNodeWithText("Review").assertDoesNotExist()
            capture("codex-task-shots", "${nextStyle.name}-$nextDark-collapsed")
            rule.onNodeWithTag("children:machine/Main task").performClick()
            rule.onNodeWithText("Review").assertIsDisplayed()
            rule.onNodeWithTag("children:machine/Review").performClick()
            rule.onNodeWithText("Regression").assertIsDisplayed()
            capture("codex-task-shots", "${nextStyle.name}-$nextDark-expanded")
            rule.onNodeWithTag("children:machine/Main task").performClick()
            rule.onNodeWithText("Review").assertDoesNotExist()
            rule.onNodeWithText("Regression").assertDoesNotExist()
        }
    }

    /** A runtime's agent and the agents it created (#4494): one works, one is done, one needs the user. */
    private fun runtimeFamily(): AgentListResult {
        fun agent(id: String, title: String, said: String, parent: String? = null, state: String = "working", agent: String = "claude") =
            AgentSession(Session(machineId = "machine", sessionId = id, title = title, cwd = "/code/app", sourceKind = "runtime",
                agent = agent, isSubagent = parent != null, parentSessionId = parent, state = state, lastText = said,
                capabilities = SessionCapabilities(read = true, send = true, interrupt = true, approve = true)))
        return buildAgentList(listOf(agent("plan", "Plan the release", "Splitting the release into three checks"),
            agent("review", "Review the plan", "Reading the release checklist", "plan", agent = "codex"),
            agent("migration", "Check the migration", "The migration runs cleanly on a copy", "plan", state = "idle"),
            agent("deploy", "Run the deploy script", "Allow running the deploy script?", "plan", state = "needs_you")))
    }

    /** The one that needs you stays a row of its own, outside "Subtasks (2)", folded and opened; one PNG each, named with [suffix]. */
    private fun captureRuntimeFamilyInEveryStyleAndTheme(suffix: String) {
        for (nextStyle in AppStyle.entries) for (nextDark in listOf(false, true)) {
            rule.runOnIdle { style = nextStyle; dark = nextDark; expanded = emptySet() }
            rule.onNodeWithText("Run the deploy script").assertIsDisplayed()
            rule.onNodeWithText("Subtasks (2)").assertIsDisplayed()
            rule.onNodeWithText("Review the plan").assertDoesNotExist()
            capture("runtime-subtask-shots", "${nextStyle.name}-$nextDark-collapsed$suffix")
            rule.onNodeWithTag("children:machine/plan").performClick()
            rule.onNodeWithText("Review the plan").assertIsDisplayed()
            rule.onNodeWithText("Check the migration").assertIsDisplayed()
            capture("runtime-subtask-shots", "${nextStyle.name}-$nextDark-expanded$suffix")
            rule.onNodeWithTag("children:machine/plan").performClick()
            rule.onNodeWithText("Review the plan").assertDoesNotExist()
            rule.onNodeWithText("Run the deploy script").assertIsDisplayed()
        }
    }

    @Test fun keepsARuntimeSubtaskThatNeedsYouInViewInEveryStyleAndTheme() {
        show(runtimeFamily())
        captureRuntimeFamilyInEveryStyleAndTheme("")
    }

    /** At the largest text size (200 %) the row that needs you and the folded "Subtasks (2)" are still in view. */
    @Test
    @Config(sdk = [35], qualifiers = "en-w400dp-h2400dp-xhdpi", fontScale = 2f)
    fun keepsARuntimeSubtaskThatNeedsYouInViewAtTheLargestTextSize() {
        show(runtimeFamily())
        captureRuntimeFamilyInEveryStyleAndTheme("-large")
    }
}
