package persona.feature.assistant

import androidx.activity.ComponentActivity
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.navigation3.runtime.NavKey
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.MutableStateFlow
import org.junit.After
import org.junit.Assert.*
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.SessionKey
import persona.core.contracts.api.*
import persona.core.designsystem.*
import persona.core.navigation.AppTab
import persona.core.navigation.Navigator

/**
 * Needs you over the blank page's composer on Glass (#4339 C17, iOS `ShellNeedsYou`): as many lines as fit between the
 * second row of things to ask and the composer (three at most, one at least), the rest's line opening Agents or the
 * Inbox, and Brief and Board keeping "Needs you now" in the page's scroll.
 */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en-w400dp-h1100dp-xhdpi")
class ShellNeedsYouScreenTest {
    @get:Rule val rule = createAndroidComposeRule<ComponentActivity>()
    private val tabs = mutableListOf<AppTab>()
    private val navigator = object : Navigator {
        override fun navigate(key: NavKey) = Unit
        override fun back() = Unit
        override fun selectTab(tab: AppTab) { tabs += tab }
    }
    private var inboxOpened = 0
    private val opened = mutableListOf<NeedsYouLine>()

    @Before fun setup() = AssistantManagerChoice.choose("hub", null, "assistant")
    @After fun cleanup() = AssistantManagerChoice.reset()

    private class Hub : AssistantRepository, MessageSender, MainConversationSender {
        // No main conversation yet: the page that starts it is the blank page.
        override val assistant = MutableStateFlow(AssistantSnapshot())
        override val outbox = MutableStateFlow(emptyList<OutboxMessage>())
        override suspend fun refresh() = Unit
        override suspend fun sendToMain(text: String, clientMessageId: String, draft: DraftSnapshot?) = Unit
        override suspend fun send(session: SessionKey, text: String, clientMessageId: String, clearDraft: String?) = Unit
        suspend fun start() = SessionKey("mac", "new")
    }

    private fun agent(id: String, what: String = "Plan ready") =
        NeedsYouLine.Agent(SessionKey("mac", id), "r-$id", "plan", null, who = id, what = what, row = "agent:mac/$id")
    private fun inbox(id: String) = NeedsYouLine.Inbox(id, lark = false, itemKind = "mail", who = id, what = "Re: $id")

    private fun mount(lines: List<NeedsYouLine>, height: State<Dp>, style: State<AppStyle> = mutableStateOf(AppStyle.Glass)) {
        val hub = Hub()
        rule.setContent {
            PersonaTheme(darkTheme = false, reduceMotion = true, style = style.value) {
                Box(Modifier.testTag("page").width(400.dp).height(height.value).background(PersonaTheme.palette.background)) {
                    AssistantScreen(navigator, hub, hub, hub::start, composerService = null, mainConversationSender = hub,
                        needsYou = NeedsYou(lines.take(NeedsYouShown), (lines.size - NeedsYouShown).coerceAtLeast(0)),
                        shellNeedsYou = lines, onOpenNeed = { opened += it }, onOpenInbox = { inboxOpened++ },
                        machines = emptyList(), phoneToolsAvailable = true)
                }
            }
        }
        rule.waitForIdle()
    }

    private fun shownLines(): Int =
        rule.onAllNodesWithTag("shell-needs-agent").fetchSemanticsNodes().size + rule.onAllNodesWithTag("shell-needs-inbox").fetchSemanticsNodes().size

    @Test fun asManyLinesAsFitBetweenTheThingsToAskAndTheComposer() {
        val lines = listOf(agent("a"), agent("b"), agent("c"), agent("d"), inbox("x"))
        val height = mutableStateOf(1100.dp)
        mount(lines, height)
        val counts = mutableMapOf<Int, Int>()
        for (h in listOf(1100, 1000, 900, 800, 700, 600)) {
            height.value = h.dp
            rule.waitForIdle()
            val shown = shownLines()
            counts[h] = shown
            assertTrue("$h dp: one at least, three at most", shown in 1..3)
            // The fold: where the second row of things to ask ends (the grid's items, two a row).
            val fold = listOf("Today's schedule", "Remind me at 5").maxOf { rule.onNodeWithText(it).getBoundsInRoot().bottom }
            val block = rule.onNodeWithTag("shell-needs-you").getBoundsInRoot()
            val line = rule.onAllNodesWithTag("shell-needs-agent")[0].getBoundsInRoot().let { it.bottom - it.top }
            val fade = Tokens.Space.xl
            // Shown lines stay clear of the fold; one more would not have been (it pushes the block up by a line).
            if (shown > 1) assertTrue("$h dp: $shown lines start below the fold", block.top >= fold + fade - 1.dp)
            if (shown < 3) assertTrue("$h dp: a line more would not fit", block.top - line < fold + fade + 1.dp)
            assertEquals("$h dp: the rest's line", 1, rule.onAllNodesWithTag("shell-needs-more").fetchSemanticsNodes().size)
        }
        assertEquals("a tall page shows three", 3, counts.getValue(1100))
        assertEquals("a short one shows one", 1, counts.getValue(600))
        // "Needs you now" is not in the scroll on Glass.
        rule.onNodeWithText("Needs you now").assertDoesNotExist()
    }

    @Test fun theRestOpenAgentsWhenOnlyAgentsAreLeftElseTheInbox() {
        val lines = mutableStateOf<List<NeedsYouLine>>(listOf(agent("a"), agent("b"), agent("c"), agent("d"), agent("e")))
        val hub = Hub()
        rule.setContent {
            PersonaTheme(darkTheme = false, reduceMotion = true, style = AppStyle.Glass) {
                Box(Modifier.width(400.dp).height(1100.dp)) {
                    AssistantScreen(navigator, hub, hub, hub::start, composerService = null, mainConversationSender = hub,
                        shellNeedsYou = lines.value, onOpenNeed = { opened += it }, onOpenInbox = { inboxOpened++ },
                        machines = emptyList(), phoneToolsAvailable = true)
                }
            }
        }
        rule.waitForIdle()
        rule.onNodeWithText("2 more need you").performClick()
        assertEquals(listOf(AppTab.Agents), tabs)
        assertEquals(0, inboxOpened)
        // A message among the rest: the Inbox, which holds only its own.
        lines.value = listOf(agent("a"), agent("b"), agent("c"), agent("d"), inbox("x"))
        rule.waitForIdle()
        rule.onNodeWithText("2 more need you").performClick()
        assertEquals(1, inboxOpened)
        lines.value = listOf(agent("a"), agent("b"), agent("c"), inbox("x"), inbox("y"))
        rule.waitForIdle()
        rule.onNodeWithText("2 more in Inbox").performClick()
        assertEquals(2, inboxOpened)
        assertEquals(listOf(AppTab.Agents), tabs)
        // A line opens where it lives.
        rule.onAllNodesWithTag("shell-needs-agent")[0].performClick()
        assertEquals("r-a", (opened.single() as NeedsYouLine.Agent).requestId)
    }

    @Test fun briefAndBoardKeepTheListInTheScroll() {
        val style = mutableStateOf(AppStyle.Brief)
        mount(listOf(agent("a"), inbox("x")), mutableStateOf(1100.dp), style)
        for (s in listOf(AppStyle.Brief, AppStyle.Board)) {
            style.value = s
            rule.waitForIdle()
            rule.onNodeWithText("Needs you now").assertExists()
            rule.onNodeWithTag("shell-needs-you").assertDoesNotExist()
        }
    }
}
