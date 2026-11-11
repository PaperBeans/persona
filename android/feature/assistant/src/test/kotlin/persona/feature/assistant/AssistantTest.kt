package persona.feature.assistant

import persona.core.contracts.Session
import persona.core.contracts.api.AssistantSnapshot
import java.time.Instant
import java.time.ZoneId
import java.util.Locale
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import kotlinx.coroutines.test.runTest
import org.junit.Assert.*
import org.junit.Test

@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
class AssistantTest {
    /** The app's own words, in Chinese (Robolectric runs in zh, docs/android/android.md). */
    private val resources get() = ApplicationProvider.getApplicationContext<android.content.Context>().resources
    private val words get() = ConversationWords(resources.getString(R.string.assistant_untitled),
        resources.getString(R.string.assistant_handoff), resources.getString(R.string.assistant_handoff_named))

    @Test fun searchSeparatesPinnedAndRecentAndIncludesMainOnlyOnce() {
        val main = Session(machineId = "demo", sessionId = "main", title = "主对话")
        val pinned = Session(machineId = "demo", sessionId = "pinned", title = "周报素材")
        val recent = Session(machineId = "demo", sessionId = "recent", title = "工作安排", lastText = "周报")
        val result = conversationGroups(AssistantSnapshot(main.key, listOf(main, pinned, recent, recent)), " 周报 ", setOf(pinned.key), words)
        assertEquals(listOf(pinned), result.pinned)
        assertEquals(listOf(recent), result.recent)
        val mainOnly = conversationGroups(AssistantSnapshot(main.key, listOf(main, main)), "主对话", setOf(main.key), words)
        assertEquals(main, mainOnly.main)
        assertTrue(mainOnly.recent.isEmpty())
        assertTrue(mainOnly.pinned.isEmpty())
        // The main conversation is the drawer's first row, above the search: a search that misses it keeps it.
        assertEquals(main, conversationGroups(AssistantSnapshot(main.key, listOf(main)), "周报", emptySet(), words).main)
        assertNull(conversationGroups(AssistantSnapshot(main.key, listOf(main.copy(archived = true))), "", emptySet(), words).main)
    }

    @Test fun historyFormatting() {
        val session = Session(machineId = "demo", sessionId = "history", title = "Claude Code",
            status = "offline", lastText = "\n继续整理安排")
        assertEquals("继续整理安排", session.conversationTitle(words))
        assertEquals(persona.core.designsystem.Status.Offline, session.conversationStatus())
        assertEquals("10月31日 17:30", conversationTime(Instant.parse("2026-10-31T09:30:00Z"), Locale.forLanguageTag("zh-Hans"), ZoneId.of("Asia/Shanghai")))
        assertEquals("31 Oct, 17:30", conversationTime(Instant.parse("2026-10-31T09:30:00Z"), Locale.forLanguageTag("en-GB"), ZoneId.of("Asia/Shanghai")))
        assertNull(conversationTime(null, Locale.ENGLISH))
    }

    @Test fun handoffConversationTitle() {
        // From the old app's InboxTest: the hub names a hand-off after its first message, a <persona-context> tag.
        val s = Session(machineId = "m", sessionId = "x", title = "<persona-context kind=\"context\" title=\"飞书 · 发布群\">(feed_id=f2)")
        assertEquals("交给 Persona：飞书 · 发布群", s.conversationTitle(words))
        val escaped = Session(machineId = "m", sessionId = "z", title = "<persona-context kind=\"context\" title=\"飞书 · a&quot;b &lt;c&gt; &amp;amp;\" url=\"\">")
        assertEquals("交给 Persona：飞书 · a\"b <c> &amp;", escaped.conversationTitle(words))
        assertEquals("早报", Session(machineId = "m", sessionId = "y", title = "早报").conversationTitle(words))
        // No title on the tag, or an empty one: still a hand-off, not the raw tag.
        assertEquals("交给 Persona", Session(machineId = "m", sessionId = "a", title = "<persona-context kind=\"context\">").conversationTitle(words))
        assertEquals("交给 Persona", Session(machineId = "m", sessionId = "b", title = "<persona-context title=\"\" url=\"\">").conversationTitle(words))
        // Untitled: the last message's first line, read before it is cut to 30 characters.
        val untitled = Session(machineId = "m", sessionId = "c", title = "Claude Code",
            lastText = "\n<persona-context kind=\"context\" title=\"项目 · 一个名字很长很长很长很长很长很长的项目\" url=\"\">\n正文")
        assertEquals("交给 Persona：项目 · 一个名字很长很长很长很长很长很长的项目", untitled.conversationTitle(words))
        assertEquals("a".repeat(30), Session(machineId = "m", sessionId = "d", lastText = "a".repeat(40)).conversationTitle(words))
        assertNull(handoffTitle("早报 <persona-context", words))
        // Bare canonical tags use the same handoff title.
        assertEquals("交给 Persona", handoffTitle(" <persona-context kind=\"context\">", words))
    }

    // #3264: the hub's own conversations leave "Recent" for "Automatic chats" (a search still finds them), pinned ones stay pinned.
    @Test fun automaticConversationsFoldAwayFromRecent() {
        val mine = Session(machineId = "demo", sessionId = "mine", title = "周报", origin = "user")
        val old = Session(machineId = "demo", sessionId = "old", title = "旧的")
        val wechat = Session(machineId = "demo", sessionId = "wechat", title = "微信里开的", origin = "channel")
        val inbox = Session(machineId = "demo", sessionId = "inbox", title = "1106 | 收件箱 | 周报", origin = "inbox", state = "needs_you")
        val task = Session(machineId = "demo", sessionId = "task", title = "1106 | 定时 | 早报", origin = "task", state = "working")
        val future = Session(machineId = "demo", sessionId = "future", title = "新来源", origin = "something_new")
        val pinnedTask = Session(machineId = "demo", sessionId = "pinned", title = "1105 | 定时 | 晚报", origin = "task")
        val all = listOf(mine, old, wechat, inbox, task, future, pinnedTask)
        val result = conversationGroups(AssistantSnapshot(null, all), "", setOf(pinnedTask.key), words)
        assertEquals(setOf(mine, old, wechat), result.recent.toSet())
        assertEquals(setOf(inbox, task, future), result.auto.toSet())
        assertEquals(listOf(pinnedTask), result.pinned)
        assertEquals(listOf(AutoOrigin.Inbox, AutoOrigin.Task, AutoOrigin.Other), autoGroups(result.auto).map { it.origin })
        assertEquals(OriginTally(null, 3, 1, 1), tally(null, result.auto))
        val searched = conversationGroups(AssistantSnapshot(null, all), "周报", emptySet(), words)
        assertEquals(setOf(mine, inbox), searched.recent.toSet())
        assertTrue(searched.auto.isEmpty())
    }

    // The leader conversations are the Directions group: first, in the hub's order, under their names, out of Pinned and Recent.
    @Test fun leadersAreTheDirectionsGroup() {
        val main = Session(machineId = "demo", sessionId = "main", title = "主对话")
        val mine = Session(machineId = "demo", sessionId = "mine", title = "周报", origin = "user")
        val leadA = Session(machineId = "demo", sessionId = "lead-a", title = "1110 | 探索 | 增长", origin = "user")
        val leadB = Session(machineId = "demo", sessionId = "lead-b", title = "1110 | 探索 | 支付", origin = "user")
        val snapshot = AssistantSnapshot(main.key, listOf(main, mine, leadB, leadA),
            leaders = listOf("lead-a", "gone", "lead-b"), directionNames = mapOf("lead-a" to "Growth"))
        val result = conversationGroups(snapshot, "", setOf(leadB.key, mine.key), words)
        assertEquals(listOf(leadA, leadB), result.directions)
        assertEquals("Growth", result.names["lead-a"])
        assertEquals(listOf(mine), result.pinned)
        assertEquals(setOf(leadB.key), result.pinnedDirections)
        assertTrue(result.recent.isEmpty())
        // a search finds a direction by its name
        assertEquals(listOf(leadA), conversationGroups(snapshot, "growth", emptySet(), words).directions)
        // no leaders: no group, as before
        assertTrue(conversationGroups(AssistantSnapshot(main.key, listOf(main, mine)), "", emptySet(), words).directions.isEmpty())
    }

    @Test fun archivedConversationsLeaveTheHistory() {
        val kept = Session(machineId = "demo", sessionId = "kept", title = "留着")
        val archived = Session(machineId = "demo", sessionId = "gone", title = "归档了", archived = true)
        val result = conversationGroups(AssistantSnapshot(null, listOf(kept, archived)), "", setOf(archived.key), words)
        assertEquals(emptyList<Session>(), result.pinned)
        assertEquals(listOf(kept), result.recent)
    }

    @Test fun theRecentGroupShowsThirtyThenTheRest() {
        val recent = (1..45).map { Session(machineId = "m", sessionId = "s$it", title = "对话 $it") }
        val folded = recentRows(recent, query = "", showAll = false)
        assertEquals(recent.take(30), folded.shown)
        assertEquals(15, folded.hidden)
        assertEquals(recent, recentRows(recent, query = "", showAll = true).shown)
        assertEquals("a search lists everything it finds", 0, recentRows(recent, query = "对话", showAll = false).hidden)
        assertEquals(0, recentRows(recent.take(30), query = "", showAll = false).hidden)
    }

    @Test fun theHistoryRowMenu() {
        fun labels(pinned: Boolean, live: Boolean, pin: Boolean, end: Boolean) =
            conversationActions(resources, pinned, live, if (pin) ({}) else null, {}, if (end) ({}) else null).map { it.label }
        assertEquals(listOf("置顶", "拷贝会话 ID", "结束对话"), labels(pinned = false, live = true, pin = true, end = true))
        assertEquals(listOf("取消置顶", "拷贝会话 ID"), labels(pinned = true, live = false, pin = true, end = true))
        assertEquals("the main conversation has no pin", listOf("拷贝会话 ID", "结束对话"), labels(pinned = false, live = true, pin = false, end = true))
        assertEquals(listOf("拷贝会话 ID"), labels(pinned = false, live = true, pin = false, end = false))
        assertTrue(conversationActions(resources, false, true, null, {}, {}).last().destructive)
        assertEquals(listOf("置顶", "拷贝会话 ID", "归档", "结束对话"),
            conversationActions(resources, false, true, {}, {}, {}, onArchive = {}).map { it.label })
    }

    @Test fun firstConversationBecomesMainAndSendUsesOutbox() = runTest {
        val repository = DemoAssistantRepository(DemoWords("周报素材", "整理这周完成的工作", "下周出差的行程", "文档的要点", "新对话"))
        val main = repository.start()
        val branch = repository.start()
        assertEquals(main, repository.assistant.value.mainSession)
        assertNotEquals(main, branch)
        repository.send(main, "整理日程", "message", null)
        repository.send(main, "整理日程", "message", null)
        assertEquals(1, repository.outbox.value.size)
        assertEquals(main, repository.outbox.value.single().session)
        assertEquals("整理日程", repository.assistant.value.conversations.single { it.key == main }.lastText)
    }
}
