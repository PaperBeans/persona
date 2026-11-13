package persona.feature.agents

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.size
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.unit.Density
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.Interaction
import persona.core.contracts.Machine
import persona.core.contracts.Session
import persona.core.designsystem.*
import persona.core.domain.sort.*
import java.io.File
import java.time.Instant

/**
 * The space page as iOS draws it (#4338 A11), in Glass light and dark: a tab of three agents (the starter working, a
 * Codex pane asking, a Claude pane done), a tab of two peers (one Awaiting reply), one working, one waiting in the
 * background; and at twice the text size, where a narrow card drops its agent chip first. PNGs in `build/space-card-shots/`.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h1000dp-xhdpi")
class SpaceCardsShotsTest {
    @get:Rule val rule = createComposeRule()

    private val now = Instant.parse("2026-11-05T10:00:00Z")
    private fun pane(id: String, title: String, state: String, slot: Int, place: Int?, words: String, agent: String = "claude",
                     name: String = "$slot", model: String? = null, awaits: Boolean = false, waiting: Boolean = false) =
        AgentSession(Session(machineId = "server", sessionId = id, title = title, cwd = "/home/me/code/app", state = state,
            agent = agent, spaceKind = "herdr", spaceId = "w1", spaceName = "app", spaceIndex = 1, slotId = "t$slot",
            slotName = name, slotIndex = slot, paneId = "w1:p$id", paneIndex = place, lastText = words, model = model,
            awaitsReply = awaits, backgroundWaiting = waiting, updatedAt = now.minusSeconds(300).toString()))

    private val rows = listOf(
        pane("W", "Move token refresh into the middleware", "working", 1, 0, "Split two panes to review and add tests; merging the middleware.", model = "anthropic/claude-opus-4"),
        pane("2E", "Review the login refactor", "needs_you", 1, 1, "Two places swallow errors. Fix them?", agent = "codex", model = "gpt-5-codex"),
        pane("31", "Tests for a failed refresh", "unread", 1, 2, "Added 6 cases, all pass.", model = "claude-sonnet-4"),
        pane("3A", "Upgrade dependencies", "working", 2, 0, "Running the full test suite.", name = "deps"),
        pane("40", "Compare two cache designs", "unread", 3, 1, "Design A has its benchmark.", name = "cache"),
        pane("41", "Benchmark cache design B", "idle", 3, 2, "B is 12% slower. Keep A?", name = "cache", awaits = true),
        pane("50", "Watch the nightly build", "working", 4, 0, "Waiting for the build to finish.", name = "watch", waiting = true),
    )
    private val machines = listOf(Machine("server", "Workstation", online = true, icon = "server", home = "/home/me"))
    private val pending = listOf(Interaction("r1", "server", "2E", kind = "question", summary = "Fix the two places?"))

    private fun capture(name: String) {
        val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
        val out = File("build/space-card-shots").also { it.mkdirs() }
        File(out, "$name.png").outputStream().use { assertTrue(bitmap.compress(Bitmap.CompressFormat.PNG, 100, it)) }
    }

    @Test fun drawsTheSpaceAsCardsInGlassLightAndDark() {
        var dark by mutableStateOf(false)
        var large by mutableStateOf(false)
        var opened by mutableStateOf<String?>(null)
        var machine by mutableStateOf<String?>(null)
        var newTab by mutableStateOf(false)
        val space = agentSpaces(rows).single()
        rule.setContent {
            val density = LocalDensity.current
            CompositionLocalProvider(LocalDensity provides Density(density.density, if (large) 2f else density.fontScale)) {
                PersonaTheme(style = AppStyle.Glass, darkTheme = dark, reduceMotion = true) {
                    Box(Modifier.testTag("shot").size(400.dp, 1000.dp).background(PersonaTheme.palette.background)) {
                        SpaceScreen(space, machines, pending, AgentsPreferencesSnapshot(), busy = false, canStart = true,
                            toast = JToastState(), now = now, onDismiss = {}, onOpen = { opened = it.session.sessionId }, onNewTab = { newTab = true },
                            spaceMenu = {}, sessionMenu = { emptyList() }, onMachine = { machine = it })
                    }
                }
            }
        }
        for (nextDark in listOf(false, true)) {
            rule.runOnIdle { dark = nextDark }
            rule.onNodeWithText("~/code/app").assertIsDisplayed()
            rule.onNodeWithText("Working · Started it").assertIsDisplayed()
            rule.onNodeWithText("Needs you · 1 pending").assertIsDisplayed()
            rule.onNodeWithText("#1 · Codex").assertIsDisplayed()
            rule.onNodeWithText("Awaiting reply").assertIsDisplayed()
            rule.onNodeWithText("Waiting in the background").assertIsDisplayed()
            rule.onNodeWithText("Open a new tab in this space").assertIsDisplayed()
            capture("space-cards-glass-${if (nextDark) "dark" else "light"}")
        }
        rule.runOnIdle { dark = false; large = true }
        // Too narrow for both chips: the tab's stays, the agent's goes.
        rule.onNodeWithText("deps").assertIsDisplayed()
        rule.onNodeWithText("Upgrade dependencies").assertIsDisplayed()
        capture("space-cards-glass-light-large")
        rule.runOnIdle { large = false }
        rule.onNodeWithText("Workstation").performClick()
        rule.onNodeWithText("Review the login refactor").performClick()
        rule.onNodeWithText("Open a new tab in this space").performClick()
        rule.runOnIdle {
            assertEquals("server", machine)
            assertEquals("2E", opened)
            assertTrue(newTab)
        }
    }
}
