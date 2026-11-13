package persona.feature.chat

import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.isDialog
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithContentDescription
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.performClick
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.Interaction
import persona.core.contracts.Item
import persona.core.contracts.Machine
import persona.core.contracts.Resolution
import persona.core.contracts.Session
import persona.core.contracts.SessionKey
import persona.core.contracts.api.ChatRepository
import persona.core.contracts.api.ChatSnapshot
import persona.core.contracts.api.InteractionCenter
import persona.core.contracts.api.MessageSender
import persona.core.contracts.api.OutboxMessage
import persona.core.contracts.api.SessionsRepository
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaTheme
import java.time.Instant

/**
 * Where an Agent's chat opens its terminal (#4339 C7, iOS `ChatView.openTerminal`): ⋯'s Terminal in every style, the
 * live work line's mark on Glass, and the header's button only on Brief and Board (decision 11).
 */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en-w400dp-h900dp")
class ChatTerminalTest {
    @get:Rule val rule = createComposeRule()

    @Test fun anAgentsChatWhoseSessionRunsInATerminalHasOne() {
        assertTrue(ChatTerminalPlaces.has(readOnly = false, canTerminal = true, ChatRenderStyle.Agent))
        assertFalse(ChatTerminalPlaces.has(readOnly = true, canTerminal = true, ChatRenderStyle.Agent))
        assertFalse(ChatTerminalPlaces.has(readOnly = false, canTerminal = false, ChatRenderStyle.Agent)) // a Codex thread
        assertFalse(ChatTerminalPlaces.has(readOnly = false, canTerminal = true, ChatRenderStyle.Assistant))
    }

    @Test fun onlyBriefAndBoardKeepTheHeadersButton() {
        assertFalse(ChatTerminalPlaces.inHeader(AppStyle.Glass))
        assertTrue(ChatTerminalPlaces.inHeader(AppStyle.Brief))
        assertTrue(ChatTerminalPlaces.inHeader(AppStyle.Board))
    }

    @Test fun theMarkIsOnTheLiveWorkLineOnGlass() {
        assertTrue(ChatTerminalPlaces.onWorkLine(live = true, AppStyle.Glass))
        assertFalse(ChatTerminalPlaces.onWorkLine(live = false, AppStyle.Glass)) // a finished turn
        assertFalse(ChatTerminalPlaces.onWorkLine(live = true, AppStyle.Brief))
        assertFalse(ChatTerminalPlaces.onWorkLine(live = true, AppStyle.Board))
    }

    private val key = SessionKey("macbook", "weather")
    private val start = Instant.now().minusSeconds(95)
    private val items = listOf(
        Item("u1", 1, role = "user", kind = "text", text = "Add a home screen widget", ts = start.toString()),
        Item("t2", 2, role = "assistant", kind = "tool_use", toolUseId = "t2", toolName = "Bash",
            toolInput = buildJsonObject { put("command", "swift test") }, ts = start.plusSeconds(5).toString()),
    )
    private val sessions = object : SessionsRepository {
        override val machines = MutableStateFlow(listOf(Machine("macbook", "MacBook", online = true)))
        override val sessions = MutableStateFlow(listOf(Session(machineId = key.machineId, sessionId = key.sessionId,
            title = "Weather widget", status = "working", agent = "claude", paneId = "p1", cwd = "/Users/me/weather")))
        override suspend fun refresh() = Unit
    }
    private val chats = object : ChatRepository {
        override fun observe(session: SessionKey) = MutableStateFlow(ChatSnapshot(items = items, loading = false))
        override suspend fun loadOlder(session: SessionKey) = Unit
        override suspend fun expandFold(session: SessionKey, fromSeq: Int, toSeq: Int): List<Item> = emptyList()
    }
    private val sender = object : MessageSender {
        override val outbox = MutableStateFlow(emptyList<OutboxMessage>())
        override suspend fun send(session: SessionKey, text: String, clientMessageId: String, clearDraft: String?) = Unit
    }
    private val interactions = object : InteractionCenter {
        override val pending = MutableStateFlow(emptyList<Interaction>())
        override suspend fun refresh() = Unit
        override suspend fun resolve(id: String, resolution: Resolution) = Unit
    }

    private val clock = Regex("\\d+m \\d+s")

    private fun texts(): List<String> = rule.onAllNodes(SemanticsMatcher("any node") { true }, useUnmergedTree = true)
        .fetchSemanticsNodes().flatMap { node ->
            node.config.getOrNull(SemanticsProperties.Text).orEmpty().map { it.text } +
                node.config.getOrNull(SemanticsProperties.ContentDescription).orEmpty()
        }

    /** The clock and the spinner never settle: frames by hand, real time for the lines built off the main thread. */
    private fun waitForWorkLine() {
        val deadline = System.currentTimeMillis() + 60_000
        while (texts().none(clock::containsMatchIn)) {
            rule.mainClock.advanceTimeByFrame()
            check(System.currentTimeMillis() < deadline) { "the work line never showed: ${texts()}" }
            Thread.sleep(20)
        }
    }

    private fun page(style: AppStyle, opened: () -> Unit) {
        rule.mainClock.autoAdvance = false
        rule.setContent {
            PersonaTheme(darkTheme = false, reduceMotion = true, style = style) {
                ChatScreen(key, chats, sender, interactions, sessions, onBack = {}, onTerminal = { assertEquals(key, it); opened() })
            }
        }
        waitForWorkLine()
    }

    @Test fun onGlassTheWorkLineAndTheMenuOpenItAndTheHeaderHasNoButton() {
        var opened = 0
        page(AppStyle.Glass) { opened++ }
        // One "Open terminal": the work line's mark (the header's button is gone).
        rule.onAllNodesWithContentDescription("Open terminal").assertCountEquals(1)
        rule.onNodeWithContentDescription("Open terminal").performClick()
        rule.mainClock.advanceTimeByFrame()
        assertEquals(1, opened)
        rule.onNodeWithContentDescription("More actions").performClick()
        repeat(5) { rule.mainClock.advanceTimeBy(200) }
        rule.onNode(hasText("Terminal") and hasAnyAncestor(isDialog())).performClick()
        rule.mainClock.advanceTimeByFrame()
        assertEquals(2, opened)
    }

    @Test fun briefKeepsItsHeaderButtonAndDrawsNoMark() {
        var opened = 0
        page(AppStyle.Brief) { opened++ }
        // The header's button; the work line draws as before, with no mark.
        rule.onAllNodesWithContentDescription("Open terminal").assertCountEquals(1)
        rule.onNodeWithContentDescription("Open terminal").performClick()
        rule.mainClock.advanceTimeByFrame()
        assertEquals(1, opened)
        rule.onNodeWithContentDescription("More actions").performClick()
        repeat(5) { rule.mainClock.advanceTimeBy(200) }
        rule.onNode(hasText("Terminal") and hasAnyAncestor(isDialog())).performClick()
        rule.mainClock.advanceTimeByFrame()
        assertEquals(2, opened)
    }

    @Test fun aChatWithNoTerminalOffersNone() {
        rule.mainClock.autoAdvance = false
        sessions.sessions.value = sessions.sessions.value.map { it.copy(sourceKind = "codex_app_server", agent = "codex", paneId = null) }
        rule.setContent {
            PersonaTheme(darkTheme = false, reduceMotion = true, style = AppStyle.Glass) {
                ChatScreen(key, chats, sender, interactions, sessions, onBack = {}, onTerminal = { error("no terminal to open") })
            }
        }
        waitForWorkLine()
        rule.onAllNodesWithContentDescription("Open terminal").assertCountEquals(0)
        rule.onNodeWithContentDescription("More actions").performClick()
        repeat(5) { rule.mainClock.advanceTimeBy(200) }
        rule.onAllNodes(hasText("Terminal") and hasAnyAncestor(isDialog())).assertCountEquals(0)
    }
}
