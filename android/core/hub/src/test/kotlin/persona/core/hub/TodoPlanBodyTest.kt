package persona.core.hub

import kotlinx.serialization.json.Json
import org.junit.Assert.assertEquals
import org.junit.Test
import persona.core.contracts.TodoDue
import persona.core.contracts.TodoPlan
import java.time.Instant
import java.time.LocalDate

/** A to-do's plan PATCH (#4636): every field sent, null clears, as iOS `TodoPlanChange` encodes it. */
class TodoPlanBodyTest {
    private fun json(s: String) = Json.parseToJsonElement(s)

    @Test fun setChangedAndCleared() {
        assertEquals(json("""{"due_at":"2026-11-20T02:00:00Z","remind_at":"2026-11-19T01:30:00Z","priority":"high"}"""),
            todoPlanBody(TodoPlan(TodoDue.At(Instant.parse("2026-11-20T02:00:00Z")), Instant.parse("2026-11-19T01:30:00Z"), "high")))
        assertEquals(json("""{"due_date":"2026-11-20","remind_at":null,"priority":"low"}"""),
            todoPlanBody(TodoPlan(TodoDue.Day(LocalDate.of(2026, 11, 20)), null, "low")))
        assertEquals(json("""{"due_at":null,"remind_at":null,"priority":""}"""), todoPlanBody(TodoPlan(null, null, "")))
    }
}
