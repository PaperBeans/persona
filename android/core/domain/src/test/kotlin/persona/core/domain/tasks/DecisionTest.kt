package persona.core.domain.tasks

import java.time.Instant
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import persona.core.contracts.AskOption
import persona.core.contracts.Todo
import persona.core.contracts.TodoAsk
import persona.core.contracts.TodoOrphaned
import persona.core.domain.zhAny

class DecisionTest {
    private val two = listOf(AskOption("Release now", "Everyone gets 2.4 today"), AskOption("Wait for the fix"))
    private fun ask(outcome: String? = "decide", options: List<AskOption>? = two, whyOwner: String? = null, default: String? = null,
        deadline: String? = null, responses: List<String>? = null, question: String? = null) =
        TodoAsk(outcome, "Needs your decision", "Release now", "2026-11-10T08:00:00Z", question = question, whyOwner = whyOwner,
            options = options, deadline = deadline, default = default, responses = responses)

    @Test fun onlyADecisionWithTwoLabelledOptionsIsAnsweredOnItsRow() {
        assertEquals(two, ask().decisionOptions)
        assertTrue(ask(options = null).decisionOptions.isEmpty()) // an older hub
        assertTrue(ask(options = listOf(AskOption("Release now"))).decisionOptions.isEmpty())
        assertTrue(ask(options = listOf(AskOption("Release now"), AskOption(" "))).decisionOptions.isEmpty())
        assertTrue(ask(outcome = "done").decisionOptions.isEmpty())
        assertTrue(ask(outcome = null).decisionOptions.isEmpty())
        // an unlabelled option is left out, the order kept: the recommended one stays first
        assertEquals(two, ask(options = listOf(two[0], AskOption(""), two[1])).decisionOptions)
    }

    @Test fun theFixedAnswersAreTheOnesTheHubTakesInOneOrder() {
        assertEquals(listOf(AskResponse.DecideYourself, AskResponse.NotWhatIAsked, AskResponse.ExplainFirst),
            ask(responses = listOf("explain_first", "later", "decide_yourself", "not_what_i_asked")).fixedResponses)
        assertEquals(listOf(AskResponse.ExplainFirst), ask(responses = listOf("explain_first")).fixedResponses)
        assertTrue(ask().fixedResponses.isEmpty())
    }

    @Test fun aStepThatCannotBeUndoneOrThatOthersSeeTakesTwoPresses() {
        assertTrue(ask(whyOwner = "irreversible").confirmsOptions)
        assertTrue(ask(whyOwner = "visible_to_others").confirmsOptions)
        assertFalse(ask(whyOwner = "priority").confirmsOptions)
        assertFalse(ask(whyOwner = "acceptance").confirmsOptions)
        assertFalse(ask().confirmsOptions)
    }

    @Test fun whatHappensWhenNobodyAnswers() {
        assertEquals(AskDefault.TakenAt(Instant.parse("2026-11-11T08:00:00Z")), ask(default = "taken", deadline = "2026-11-11T08:00:00Z").fallback)
        assertEquals(AskDefault.Never, ask(default = "never", deadline = "2026-11-11T08:00:00Z").fallback)
        assertNull(ask(default = "taken").fallback) // no moment to say
        assertNull(ask(deadline = "2026-11-11T08:00:00Z").fallback)
        assertNull(ask(default = "later").fallback)
    }

    @Test fun theQuestionIsTheDecisionsOwnSentenceElseItsVerdict() {
        assertEquals("Release 2.4 now?", ask(question = "Release 2.4 now?").asked)
        assertEquals("Needs your decision", ask().asked)
        assertEquals("Needs your decision", ask(question = " ").asked)
    }

    @Test fun anOpenToDoSaysNobodyIsWorkingOnItAndWhy() {
        fun line(reason: String?, done: Boolean = false) =
            Todo("t", doneAt = "2026-11-10T08:00:00Z".takeIf { done }, orphaned = TodoOrphaned(reason)).orphanedLine.zhAny()
        assertEquals("没人在做 · 出错后关闭", line("error"))
        assertEquals("没人在做 · 电脑离线", line("offline"))
        assertEquals("没人在做 · 已关闭", line("closed"))
        // a reason this app does not know, or none, leaves the lead alone
        assertEquals("没人在做", line("paused"))
        assertEquals("没人在做", line(null))
        assertNull(line("error", done = true))
        assertNull(Todo("t").orphanedLine)
    }
}
