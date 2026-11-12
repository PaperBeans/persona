package persona.core.domain.models

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertSame
import org.junit.Test
import persona.core.contracts.ModelPick
import persona.core.contracts.PendingSwitch
import persona.core.contracts.Session
import persona.core.contracts.SessionCompaction

/**
 * A summary row landing on a conversation the app holds (#5055, riba2534's note on #5055; iOS `preservingDetails`):
 * what it leaves out stays, what it carries (the model, the effort, `model_pick`) is its own.
 */
class SummaryRowsTest {
    private val full = Session(machineId = "mac", sessionId = "c1", assistant = true, launcher = "team", permissionMode = "plan",
        effort = "high", model = "sonnet", compaction = SessionCompaction("done"), modelPick = ModelPick("team", "sonnet"),
        pendingSwitch = PendingSwitch("work"), runtime = "agentsdk")

    @Test fun aSummaryKeepsTheChatsDetailsAndBringsItsOwnPick() {
        val summary = Session(machineId = "mac", sessionId = "c1", assistant = true, waiting = false, model = "opus", effort = "low",
            modelPick = ModelPick("claude", "opus"))
        val merged = summary.keepingDetails(full)
        assertEquals("team", merged.launcher)
        assertEquals("plan", merged.permissionMode)
        assertEquals(SessionCompaction("done"), merged.compaction)
        assertEquals("agentsdk", merged.runtime)
        assertEquals(PendingSwitch("work"), merged.pendingSwitch)
        assertEquals("opus", merged.model)
        assertEquals("low", merged.effort)
        assertEquals(ModelPick("claude", "opus"), merged.modelPick)
    }

    @Test fun aSummaryWithoutAPickMeansItFollowsTheDefault() {
        val summary = Session(machineId = "mac", sessionId = "c1", assistant = true, waiting = false)
        assertNull(summary.keepingDetails(full).modelPick)
        assertEquals("high", summary.keepingDetails(full).effort)
    }

    @Test fun aCompleteRowOrNoneHeldIsTakenAsItIs() {
        val complete = Session(machineId = "mac", sessionId = "c1", assistant = true)
        assertSame(complete, complete.keepingDetails(full))
        val summary = complete.copy(waiting = true)
        assertSame(summary, summary.keepingDetails(null))
        assertSame(summary, summary.keepingDetails(full.copy(sessionId = "other")))
    }
}
