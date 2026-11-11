package persona.platform.capsule

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.ScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Text
import androidx.compose.runtime.getValue
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.assertHeightIsEqualTo
import androidx.compose.ui.test.assertWidthIsEqualTo
import androidx.compose.ui.test.assertTextContains
import androidx.compose.ui.test.assertIsFocused
import androidx.compose.ui.test.assertIsNotFocused
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsNotDisplayed
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextInput
import androidx.compose.ui.test.performScrollTo
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.designsystem.CapsuleTokens
import persona.core.designsystem.PersonaTheme

@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "zh-w411dp-h900dp-xhdpi")
class CapsuleSurfaceTest {
    @get:Rule val rule = createComposeRule()
    private val sections = listOf(CapsuleSection("unread", "未读", 3),
        CapsuleSection("main", "主对话", list = false), CapsuleSection("inbox", "收件箱", 6))
    private val unread = CapsuleEntry("agent", "A very long task title", "A long preview that belongs in the list", CapsuleState.Done)
    private val main = CapsuleEntry("main-chat", "Persona", "", CapsuleState.Idle)
    private val inbox = CapsuleEntry("inbox-item", "A request in the inbox", "Open its original detail", CapsuleState.NeedsYou)

    private fun selectSection(id: String) {
        // The title opens the grouped list (whose chips switch sections) when there is one, else the switch menu.
        if (rule.onAllNodesWithTag("capsule.section.$id").fetchSemanticsNodes().isEmpty()) {
            rule.onNodeWithTag("capsule.switch").performClick()
            rule.waitForIdle()
        }
        if (rule.onAllNodesWithTag("capsule.section.$id").fetchSemanticsNodes().isNotEmpty())
            rule.onNodeWithTag("capsule.section.$id").performClick()
    }

    private fun collapse() {
        rule.onNodeWithTag("capsule.more").performClick()
        rule.onNodeWithTag("capsule.collapse").performClick()
    }

    @Test fun firstNonEmptyMessageLoadShowsTheLatestReplyAndPreservesManualHistoryScrollOnUpdates() {
        val drafts = mutableStateMapOf<Pair<String?, String?>, String>()
        val viewed = mutableListOf<String>()
        val history = (1..7).map { index ->
            CapsuleMessage("history-$index", "History $index\n" + "An earlier reply with several lines.\n".repeat(6), false)
        }
        val latest = CapsuleMessage("latest", "The latest assistant reply", false)
        val next = CapsuleMessage("next", "A new reply while reading history", false)
        val final = CapsuleMessage("final", "A newer reply while following the bottom", false)
        var content by mutableStateOf(CapsuleContent(entries = listOf(main), selectedId = main.id,
            sections = sections, sectionId = "main"))
        rule.setContent { PersonaTheme(reduceMotion = true) {
            CapsuleSurface(content, true, {}, {}, {}, {}, {}, { _, _ -> }, { _, _ -> },
                onViewed = { _, message, _ -> viewed += message; true }, windowFocused = true, drafts = drafts)
        } }
        rule.runOnIdle { content = content.copy(messages = history + latest, readVersion = "v1") }
        // Replies render as Markdown, laid out asynchronously: wait for the follow-to-bottom to settle.
        rule.waitUntil(5_000) { runCatching { rule.onNodeWithText(latest.text).assertIsDisplayed() }.isSuccess }
        rule.onNodeWithText(latest.text).assertIsDisplayed()
        rule.waitForIdle()
        rule.mainClock.advanceTimeBy(1_000)
        rule.runOnIdle { assertEquals(listOf(latest.id), viewed) }
        // Agent replies render as Markdown, which joins soft line breaks: find the message by its tag.
        rule.onNodeWithTag("capsule.message.${history[3].id}").performScrollTo()
        rule.onNodeWithText(latest.text).assertIsNotDisplayed()
        rule.runOnIdle { content = content.copy(messages = content.messages + next, readVersion = "v2") }
        rule.waitForIdle()
        rule.mainClock.advanceTimeBy(1_000)
        rule.onNodeWithText(next.text).assertIsNotDisplayed()
        rule.runOnIdle { assertEquals(listOf(latest.id), viewed) }
        rule.onNodeWithText(next.text).performScrollTo()
        rule.onNodeWithText(next.text).assertIsDisplayed()
        rule.runOnIdle { content = content.copy(messages = content.messages + final, readVersion = "v3") }
        rule.waitUntil(5_000) { runCatching { rule.onNodeWithText(final.text).assertIsDisplayed() }.isSuccess }
        rule.onNodeWithText(final.text).assertIsDisplayed()
        rule.waitForIdle()
        rule.mainClock.advanceTimeBy(1_000)
        rule.runOnIdle { assertEquals(final.id, viewed.last()) }
    }

    @Test fun oneTapWaitsForTheCurrentChatAndWindowFocusThenAcceptsAReplyWithoutAnotherTap() {
        val drafts = mutableStateMapOf<Pair<String?, String?>, String>()
        var expanded by mutableStateOf(false)
        var focused by mutableStateOf(false)
        var content by mutableStateOf(CapsuleContent(entries = listOf(unread), sections = sections, sectionId = "unread"))
        var sent: Pair<String?, String>? = null
        rule.setContent { PersonaTheme(reduceMotion = true) {
            CapsuleSurface(content, expanded, onExpand = { expanded = true }, onCollapse = { expanded = false },
                onSelect = {}, onSelectSection = {}, onOpen = {}, onSend = { id, text -> sent = id to text },
                onAction = { _, _ -> }, onViewed = { _, _, _ -> true }, windowFocused = focused, drafts = drafts)
        } }
        rule.onNodeWithTag("capsule.orb").performClick()
        rule.onNodeWithTag("capsule.input").assertDoesNotExist()
        rule.runOnIdle { content = content.copy(entries = listOf(main), selectedId = main.id, sectionId = "main") }
        rule.onNode(hasSetTextAction()).assertIsNotFocused()
        rule.runOnIdle { focused = true }
        rule.onNode(hasSetTextAction()).assertIsFocused().performTextInput("Reply to this conversation")
        rule.onNodeWithTag("capsule.send").performClick()
        rule.waitForIdle()
        assertEquals(main.id to "Reply to this conversation", sent)
        rule.runOnIdle { content = content.copy(sectionId = "unread", selectedId = null, entries = listOf(unread)) }
        rule.onNode(hasSetTextAction()).assertDoesNotExist()
    }

    @Test fun onlyAnAssistantReplyActuallyInsideTheViewportIsAcknowledged() {
        val drafts = mutableStateMapOf<Pair<String?, String?>, String>()
        val viewed = mutableListOf<Pair<String?, String>>()
        val scroll = ScrollState(0)
        var content by mutableStateOf(CapsuleContent(entries = listOf(main), selectedId = main.id,
            sections = sections, sectionId = "main", canSend = false, readVersion = "v1", messages = listOf(CapsuleMessage("reply", "Visible assistant reply", false))))
        rule.setContent { PersonaTheme(reduceMotion = true) {
            Column(Modifier.height(240.dp).verticalScroll(scroll)) {
                Spacer(Modifier.height(600.dp))
                CapsuleSurface(content, true, {}, {}, {}, {}, {}, { _, _ -> }, { _, _ -> },
                    onViewed = { id, message, _ -> viewed += id to message; true }, windowFocused = true, drafts = drafts)
            }
        } }
        rule.mainClock.advanceTimeBy(1_000)
        rule.runOnIdle { assertTrue(viewed.isEmpty()) }
        rule.runOnIdle { runBlocking { scroll.scrollTo(with(rule.density) { 600.dp.roundToPx() }) } }
        rule.onNodeWithText("Visible assistant reply").assertIsDisplayed()
        rule.waitForIdle()
        rule.mainClock.advanceTimeBy(1_000)
        rule.runOnIdle { assertEquals(listOf(main.id to "reply"), viewed) }
        rule.runOnIdle { content = content.copy(messages = content.messages + CapsuleMessage("user", "A user message", true)) }
        rule.mainClock.advanceTimeBy(1_000)
        rule.runOnIdle { assertEquals(1, viewed.size) }
    }

    @Test fun aReplyWaitsForVerificationAndARejectedVersionCanBeAcknowledgedLater() {
        val drafts = mutableStateMapOf<Pair<String?, String?>, String>()
        val attempts = mutableListOf<String>()
        var content by mutableStateOf(CapsuleContent(entries = listOf(main), selectedId = main.id,
            sections = sections, sectionId = "main", messages = listOf(CapsuleMessage("reply", "Reply awaiting verification", false))))
        rule.setContent { PersonaTheme(reduceMotion = true) {
            CapsuleSurface(content, true, {}, {}, {}, {}, {}, { _, _ -> }, { _, _ -> },
                onViewed = { _, message, version ->
                    assertEquals("reply", message)
                    attempts += version
                    version == "verified-2"
                }, windowFocused = true, drafts = drafts)
        } }
        rule.onNodeWithText("Reply awaiting verification").assertIsDisplayed()
        rule.waitForIdle()
        rule.mainClock.advanceTimeBy(1_000)
        rule.runOnIdle { assertTrue(attempts.isEmpty()); content = content.copy(readVersion = "verified-1") }
        rule.waitForIdle()
        rule.mainClock.advanceTimeBy(1_000)
        rule.runOnIdle { assertEquals(listOf("verified-1"), attempts); content = content.copy(readVersion = null) }
        rule.waitForIdle()
        rule.mainClock.advanceTimeBy(1_000)
        rule.runOnIdle { content = content.copy(readVersion = "verified-2") }
        rule.waitForIdle()
        rule.mainClock.advanceTimeBy(1_000)
        rule.runOnIdle { assertEquals(listOf("verified-1", "verified-2"), attempts) }
        rule.runOnIdle { content = content.copy(readVersion = null) }
        rule.waitForIdle()
        rule.mainClock.advanceTimeBy(1_000)
        rule.runOnIdle { content = content.copy(readVersion = "verified-2") }
        rule.waitForIdle()
        rule.mainClock.advanceTimeBy(1_000)
        rule.runOnIdle { assertEquals(listOf("verified-1", "verified-2"), attempts) }
    }

    @Test fun collapsingBeforeTheReadDelayNeverAcknowledgesTheReplyFromTheCompactView() {
        val drafts = mutableStateMapOf<Pair<String?, String?>, String>()
        var expanded by mutableStateOf(true)
        val viewed = mutableListOf<String>()
        rule.setContent { PersonaTheme(reduceMotion = true) {
            CapsuleSurface(CapsuleContent(entries = listOf(main), selectedId = main.id,
                sections = sections, sectionId = "main", readVersion = "v1", messages = listOf(CapsuleMessage("reply", "Unread reply", false))),
                expanded, {}, { expanded = false }, {}, {}, {}, { _, _ -> }, { _, _ -> },
                onViewed = { _, message, _ -> viewed += message; true }, windowFocused = true, drafts = drafts)
        } }
        rule.onNodeWithText("Unread reply").assertIsDisplayed()
        collapse()
        rule.mainClock.advanceTimeBy(1_000)
        rule.onNodeWithTag("capsule.orb").assertExists()
        rule.runOnIdle { assertTrue(viewed.isEmpty()) }
    }

    @OptIn(ExperimentalCoroutinesApi::class)
    @Test fun conversationDraftsSurviveACompletionReplacingTheCollapsedSurface() {
        val timerScope = TestScope()
        val completions = CapsuleCompletionQueue(timerScope)
        completions.setVisible(true)
        completions.setManual(true)
        val drafts = mutableStateMapOf<Pair<String?, String?>, String>()
        var expanded by mutableStateOf(true)
        var content by mutableStateOf(CapsuleContent(entries = listOf(main), selectedId = main.id,
            sections = sections, sectionId = "main"))
        rule.setContent {
            PersonaTheme(reduceMotion = true) {
                val completion by completions.current.collectAsState()
                if (!expanded && completion != null) {
                    CapsuleCompletionNotice(completion!!) { }
                } else {
                    CapsuleSurface(content, expanded, onExpand = {
                        expanded = true
                        completions.setManual(true)
                    }, onCollapse = {
                        expanded = false
                        completions.setManual(false)
                    }, onSelect = { id -> content = content.copy(selectedId = id) }, onSelectSection = { id ->
                        // 未读 opens its list (pick a conversation from it); 主对话 opens the main chat.
                        content = if (id == "main") content.copy(sectionId = id, selectedId = main.id, entries = listOf(main))
                            else content.copy(sectionId = id, selectedId = null, entries = listOf(unread))
                    }, onOpen = {}, onSend = { _, _ -> }, onAction = { _, _ -> }, onViewed = { _, _, _ -> true }, windowFocused = false, drafts = drafts)
                }
            }
        }
        rule.onNode(hasSetTextAction()).performTextInput("Keep the main draft")
        selectSection("unread")
        rule.onNodeWithTag("capsule.entry.${unread.id}").performClick()
        rule.onNode(hasSetTextAction()).performTextInput("Keep the agent draft")
        selectSection("main")
        rule.onNode(hasSetTextAction()).assertTextContains("Keep the main draft")
        collapse()
        rule.runOnIdle { completions.offer(CapsuleCompletion("done", unread.id, unread.title)) }
        rule.onNodeWithTag("capsule.completion").assertExists()
        rule.onNodeWithTag("capsule.orb").assertDoesNotExist()
        rule.runOnIdle { timerScope.runCurrent(); timerScope.advanceTimeBy(4_000); timerScope.runCurrent() }
        rule.onNodeWithTag("capsule.completion").assertDoesNotExist()
        rule.onNodeWithTag("capsule.orb").performClick()
        rule.onNode(hasSetTextAction()).assertTextContains("Keep the main draft")
        selectSection("unread")
        rule.onNodeWithTag("capsule.entry.${unread.id}").performClick()
        rule.onNode(hasSetTextAction()).assertTextContains("Keep the agent draft")
        rule.runOnIdle { completions.close() }
    }

    @Test fun collapsedCapsuleKeepsItsSizeAndHidesAllTaskTextAndNumbers() {
        val drafts = mutableStateMapOf<Pair<String?, String?>, String>()
        rule.setContent {
            PersonaTheme(reduceMotion = true) {
                CapsuleSurface(CapsuleContent(entries = listOf(unread), selectedId = unread.id,
                    sections = sections, sectionId = "unread", badgeCount = 15, hasActivity = true), false,
                    {}, {}, {}, {}, {}, { _, _ -> }, { _, _ -> }, onViewed = { _, _, _ -> true }, windowFocused = false, drafts = drafts,
                    extraContent = { Text("Extension content") })
            }
        }
        rule.onNodeWithTag("capsule.orb").assertWidthIsEqualTo(CapsuleTokens.compactWidth).assertHeightIsEqualTo(CapsuleTokens.orb)
        rule.onNodeWithTag("capsule.badge", useUnmergedTree = true).assertExists()
        rule.onNodeWithText(unread.title).assertDoesNotExist()
        rule.onNodeWithText(unread.summary).assertDoesNotExist()
        rule.onNodeWithText("15").assertDoesNotExist()
        rule.onNodeWithText("3/15").assertDoesNotExist()
        rule.onNodeWithText("Extension content").assertDoesNotExist()
    }

    @Test fun staleInboxSelectionShowsTheProviderErrorInsideTheList() {
        val drafts = mutableStateMapOf<Pair<String?, String?>, String>()
        rule.setContent {
            PersonaTheme(reduceMotion = true) {
                CapsuleSurface(CapsuleContent(entries = listOf(inbox), sections = sections, sectionId = "inbox"), true,
                    {}, {}, { throw IllegalStateException("事项已不在当前分组") }, {}, {}, { _, _ -> }, { _, _ -> }, onViewed = { _, _, _ -> true }, windowFocused = false, drafts = drafts)
            }
        }
        rule.onNodeWithTag("capsule.entry.inbox-item").performClick()
        rule.waitForIdle()
        rule.onNodeWithText("事项已不在当前分组").assertExists()
        rule.onNodeWithTag("capsule.list").assertExists()
    }

    @Test fun fixedSectionsSwitchBetweenAListAndChatWithoutLeakingComposerOrExtensionContent() {
        var content by mutableStateOf(CapsuleContent(entries = listOf(unread), sections = sections, sectionId = "unread"))
        var opened: String? = null
        var sent: Pair<String?, String>? = null
        val drafts = mutableStateMapOf<Pair<String?, String?>, String>()
        rule.setContent {
            PersonaTheme(reduceMotion = true) {
                Box(Modifier.heightIn(max = 420.dp)) {
                    CapsuleSurface(content, true, {}, {}, onSelect = { id ->
                        if (content.sectionId == "inbox") opened = id
                        else content = content.copy(selectedId = id, messages = if (id != null)
                            listOf(CapsuleMessage("reply", "The actual reply", false)) else emptyList())
                    }, onSelectSection = { id ->
                        content = content.copy(sectionId = id, selectedId = if (id == "main") main.id else null,
                            entries = when (id) { "main" -> listOf(main); "inbox" -> listOf(inbox); else -> listOf(unread) }, messages = emptyList())
                    }, onOpen = { opened = it }, onSend = { id, text -> sent = id to text }, onAction = { _, _ -> }, onViewed = { _, _, _ -> true }, windowFocused = false, drafts = drafts,
                        extraContent = { Text("Extension content", Modifier.testTag("extension")) })
                }
            }
        }
        rule.onNodeWithTag("capsule.card").assertWidthIsEqualTo(340.dp)
        rule.onNodeWithTag("capsule.section.unread").assertExists()
        rule.onNodeWithText("未读 3").assertExists()
        rule.onNodeWithText("主对话").assertExists()
        rule.onNodeWithText("收件箱 6").assertExists()
        rule.onNodeWithTag("capsule.input").assertDoesNotExist()
        rule.onNodeWithTag("extension").assertDoesNotExist()
        rule.onNodeWithTag("capsule.entry.agent").performClick()
        // Replies render as Markdown, laid out asynchronously.
        rule.waitUntil(5_000) { rule.onAllNodesWithText("The actual reply").fetchSemanticsNodes().isNotEmpty() }
        rule.onNodeWithText("The actual reply").assertExists()
        rule.onNodeWithTag("capsule.back").assertExists().performClick()
        rule.onNodeWithTag("capsule.list").assertExists()
        rule.onNodeWithTag("capsule.input").assertDoesNotExist()
        selectSection("main")
        rule.onNodeWithTag("capsule.back").assertDoesNotExist()
        rule.onNodeWithTag("extension").assertExists()
        rule.onNode(hasSetTextAction()).performTextInput("Continue the main conversation")
        rule.onNodeWithTag("capsule.send").performClick()
        rule.waitForIdle()
        assertEquals(main.id to "Continue the main conversation", sent)
        selectSection("inbox")
        rule.onNodeWithTag("capsule.input").assertDoesNotExist()
        rule.onNodeWithTag("extension").assertDoesNotExist()
        rule.onNodeWithTag("capsule.entry.inbox-item").performClick()
        assertEquals(inbox.id, opened)
        assertTrue(content.listing)
    }
}
