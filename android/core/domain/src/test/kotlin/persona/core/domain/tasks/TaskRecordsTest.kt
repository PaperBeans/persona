package persona.core.domain.tasks

import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import org.junit.Assert.assertEquals
import org.junit.Test
import persona.core.contracts.FeedItem
import persona.core.contracts.JsonCodec
import persona.core.contracts.PlanTask
import persona.core.contracts.TaskPlan
import persona.core.contracts.Todo
import persona.core.domain.lines.ChatLine

/**
 * #4339 C13: on Glass the conversation draws the hub's task records as iOS's `FeedLine` does, message cards included; only
 * plans and task cards keep their other places. Brief and Board keep every record behind the trays, as before.
 */
class TaskRecordsTest {
    private fun at(n: Int) = "2026-11-12T10:00:${n.toString().padStart(2, '0')}Z"
    private val plan = TaskPlan("plan", "main", listOf(PlanTask("Fix the login", todoId = "t")), "started", at(1))
    private val todos = listOf(Todo("t", "Fix the login", origin = "main", planId = "plan", createdAt = at(1)))
    private fun item(id: String, n: Int, kind: String, ref: String? = "t", vararg fields: Pair<String, String>) =
        FeedItem(id, "main", at(n), kind, ref, buildJsonObject { fields.forEach { (k, v) -> put(k, v) } })
    private val feed = listOf(
        item("plan-feed", 1, "plan", "plan"),
        item("task-feed", 2, "task"),
        item("handoff", 3, "routed", "t", "from" to "assistant", "text" to "Fix the login crash"),
        item("asked", 4, "routed", "t", "text" to "Use the staging key"),
        item("report", 5, "report", "t", "kind" to "question", "text" to "Which key?"),
        item("review", 6, "review", "t", "verdict" to "rework"),
        item("receipt", 7, "receipt", "t", "title" to "Fix the login", "summary" to "Done"),
        item("notice", 8, "notice", null, "text" to "The morning brief runs again"),
        item("message", 9, "message", "inbox-1", "text" to "When is it fixed?", "draft" to "Today"),
    )
    private fun feedIds(lines: List<ChatLine>) =
        lines.filterIsInstance<ChatLine.Extra>().mapNotNull { (it.payload as? AssistantExtra.Feed)?.item?.id }
    private fun hints(lines: List<ChatLine>) = lines.filterIsInstance<ChatLine.Extra>().mapNotNull { it.payload as? DispatchHint }

    @Test fun glassDrawsTheRecordsAmongTheLines() {
        val lines = AssistantTaskCards.lines(emptyList(), feed, mapOf(plan.id to plan), todos, inline = true)
        assertEquals(listOf("handoff", "asked", "report", "review", "receipt", "notice", "message"), feedIds(lines))
        // the plan and the task card are the dispatched line, once
        assertEquals(listOf(listOf("Fix the login")), hints(lines).map { hint -> hint.tasks.map { it.title } })
    }

    @Test fun glassKeepsOnlyPlansAndTaskCardsBehind() {
        assertEquals(listOf("plan-feed", "task-feed"), AssistantTaskCards.backgroundFeed(feed, inline = true).map { it.id })
    }

    @Test fun briefAndBoardKeepTheRecordsBehindTheTrays() {
        val lines = AssistantTaskCards.lines(emptyList(), feed, mapOf(plan.id to plan), todos)
        // the user's own word to the task stays, as before
        assertEquals(listOf("asked"), feedIds(lines))
        assertEquals(listOf("plan-feed", "task-feed", "handoff", "report", "review", "receipt", "notice", "message"),
            AssistantTaskCards.backgroundFeed(feed).map { it.id })
    }

    @Test fun aNoticeKeepsItsKindOnTheWire() {
        val decoded = JsonCodec.decodeFromString(FeedItem.serializer(),
            """{"id":"n","conversation":"main","at":"${at(8)}","kind":"notice","body":{"text":"Ready again"}}""")
        assertEquals("notice", decoded.kind)
        assertEquals("Ready again", decoded.field("text"))
        // a kind this build doesn't know still reads as unknown
        assertEquals("unknown", JsonCodec.decodeFromString(FeedItem.serializer(), """{"id":"x","kind":"later_kind"}""").kind)
    }
}
