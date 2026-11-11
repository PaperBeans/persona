package persona.core.designsystem

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.Workflow
import persona.core.domain.sort.WorkflowSummary

/** A workflow card's "done of started" is worded in the English plural form of the total, done first (#4080). */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en")
class WorkflowAgentsDoneTest {
    private val res get() = ApplicationProvider.getApplicationContext<Context>().resources

    private fun spoken(done: Int, started: Int) = WorkflowSummary.agentsDone(Workflow(agentsDone = done, agentsStarted = started)).resolve(res)

    @Test fun singularAndPlural() {
        assertEquals("0/1 agent done", spoken(0, 1))
        assertEquals("1/1 agent done", spoken(1, 1))
        assertEquals("1/2 agents done", spoken(1, 2))
        assertEquals("169/185 agents done", spoken(169, 185))
    }
}
