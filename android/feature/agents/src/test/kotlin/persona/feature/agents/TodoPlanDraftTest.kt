package persona.feature.agents

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import persona.core.contracts.Todo
import persona.core.contracts.TodoDue
import persona.core.contracts.TodoPlan
import java.time.*

/** The detail page's due date, reminder and priority (#4636), as iOS `TodoPlanDraft`. */
class TodoPlanDraftTest {
    private val zone = ZoneId.of("Asia/Shanghai")
    private val now = ZonedDateTime.of(2026, 11, 11, 14, 25, 0, 0, zone)

    @Test fun aToDoWithoutAPlanStartsTomorrowEveningAndAtTheNextHour() {
        val d = TodoPlanDraft.of(Todo("t"), now)
        assertFalse(d.hasDue)
        assertTrue(d.allDay)
        assertEquals(LocalDateTime.of(2026, 11, 12, 18, 0), d.due)
        assertEquals(LocalDateTime.of(2026, 11, 11, 15, 0), d.remind)
        assertEquals(TodoPlan(null, null, ""), d.plan(zone))
    }

    @Test fun aPlanIsReadAndWrittenBackAsTheHubKeepsIt() {
        val at = TodoPlanDraft.of(Todo("t", dueAt = "2026-11-20T02:00:00Z", remindAt = "2026-11-19T01:30:00Z", priority = "high"), now)
        assertTrue(at.hasDue && !at.allDay && at.hasRemind)
        assertEquals(TodoPlan(TodoDue.At(Instant.parse("2026-11-20T02:00:00Z")), Instant.parse("2026-11-19T01:30:00Z"), "high"), at.plan(zone))
        val day = TodoPlanDraft.of(Todo("t", dueDate = "2026-11-20", priority = "low"), now)
        assertTrue(day.hasDue && day.allDay)
        assertEquals(TodoPlan(TodoDue.Day(LocalDate.of(2026, 11, 20)), null, "low"), day.plan(zone))
        // switching all-day off keeps the picked day, at 18:00 local
        assertEquals(TodoDue.At(Instant.parse("2026-11-20T10:00:00Z")), day.copy(allDay = false).plan(zone).due)
        // a switch that is off doesn't count its date
        assertEquals(TodoPlan(null, null, "low"), day.copy(hasDue = false, remind = LocalDateTime.of(2030, 1, 1, 0, 0)).plan(zone))
    }
}
