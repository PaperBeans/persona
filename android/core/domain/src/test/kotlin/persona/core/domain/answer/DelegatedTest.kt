package persona.core.domain.answer

import persona.core.contracts.DelegatedAnswer
import persona.core.contracts.Interaction
import persona.core.contracts.InteractionRecord
import persona.core.contracts.Question
import persona.core.contracts.QuestionOption
import persona.core.contracts.Resolution
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/** iOS `DelegateTests`' pure cases: the history, Change answer's form and what of it goes to the Agent. */
class DelegatedTest {
    private fun q(question: String, labels: List<String>, multi: Boolean = false) =
        Question(question, multiSelect = multi, options = labels.map { QuestionOption(it) })

    /** iOS `Delegated.history`: resolved questions the assistant answered, newest first; other cards and answers left out. */
    @Test fun theHistoryListsWhatTheAssistantAnswered() {
        fun record(id: String, kind: String = "question", status: String = "resolved", by: String? = "assistant", at: String? = null) =
            InteractionRecord(Interaction(id, kind = kind), status, Resolution(answers = mapOf("Which?" to "A"), answeredBy = by, reason = "why $id"), at)
        val answers = Delegated.history(listOf(
            record("old", at = "2026-11-10T01:00:00Z"), record("mine", by = null), record("plan", kind = "plan"),
            record("expired", status = "expired"), record("new", at = "2026-11-12T01:00:00Z"), record("undated"),
        ))
        assertEquals(listOf("new", "old", "undated"), answers.map { it.id })
        assertEquals("why new", answers.first().reason)
        assertEquals(mapOf("Which?" to "A"), answers.first().answers)
    }

    @Test fun theAnswersNowAreTheAssistantsWithTheUsersChanges() {
        val d = DelegatedAnswer(Interaction("ix"), mapOf("Driver?" to "pgx", "Test first?" to "Yes"), "", null)
        assertEquals(d.answers, Delegated.latest(d, null))
        assertEquals(mapOf("Driver?" to "sqlc", "Test first?" to "Yes"), Delegated.latest(d, mapOf("Driver?" to "sqlc")))
    }

    @Test fun theFormStartsFromTheAnswersSoFar() {
        val qs = listOf(q("Driver?", listOf("pgx", "sqlc")), q("Which ones?", listOf("A", "B"), multi = true), q("Note?", listOf("None")))
        val start = ReanswerForm.start(qs, mapOf("Driver?" to "pgx", "Which ones?" to "A, B", "Note?" to "Before Friday"))
        assertEquals(mapOf(0 to listOf("pgx"), 1 to listOf("A", "B"), 2 to emptyList()), start.picked)
        assertEquals(mapOf(2 to "Before Friday"), start.custom)
        assertEquals(mapOf("Driver?" to "sqlc", "Which ones?" to "B, A", "Note?" to "Next week"),
            ReanswerForm(mapOf(0 to listOf("sqlc"), 1 to listOf("B", "A")), mapOf(2 to " Next week ")).answers(qs))
    }

    @Test fun pickingAndTypingReplaceEachOther() {
        val single = q("Driver?", listOf("pgx", "sqlc"))
        val multi = q("Which ones?", listOf("A", "B"), multi = true)
        var form = ReanswerForm(mapOf(0 to listOf("pgx")), mapOf(0 to ""))
        form = form.toggle(0, single, "sqlc")
        assertEquals(listOf("sqlc"), form.picked[0])
        // The one picked again: none.
        assertEquals(emptyList<String>(), form.toggle(0, single, "sqlc").picked[0])
        form = form.typed(0, "Neither")
        assertEquals(emptyList<String>(), form.picked[0])
        assertEquals("Neither", form.custom[0])
        // A pick clears the words; blank words leave the picks.
        form = form.toggle(0, single, "pgx")
        assertEquals("", form.custom[0])
        assertEquals(listOf("pgx"), form.typed(0, "  ").picked[0])
        // A multi-select toggles each.
        val many = ReanswerForm().toggle(1, multi, "A").toggle(1, multi, "B").toggle(1, multi, "A")
        assertEquals(listOf("B"), many.picked[1])
    }

    @Test fun onlyAnswersThatSaySomethingElseGo() {
        assertTrue(Delegated.changedAnswers(mapOf("Lib?" to "pgx"), mapOf("Lib?" to " pgx ")).isEmpty())
        assertEquals(mapOf("Lib?" to "sqlc"), Delegated.changedAnswers(mapOf("Lib?" to "pgx"), mapOf("Lib?" to "sqlc")))
        assertTrue("an emptied answer is no answer", Delegated.changedAnswers(mapOf("Lib?" to "pgx"), mapOf("Lib?" to "")).isEmpty())
        val multi = listOf(q("Which?", listOf("A", "B", "C"), multi = true))
        assertTrue(Delegated.changedAnswers(mapOf("Which?" to "A, B"), mapOf("Which?" to "B, A"), multi).isEmpty())
        assertEquals(mapOf("Which?" to "A, C"), Delegated.changedAnswers(mapOf("Which?" to "A, B"), mapOf("Which?" to "A, C"), multi))
        // A single choice whose label has a comma, and words of the user's own, are compared as written.
        assertFalse(Delegated.changedAnswers(mapOf("Lib?" to "A, B"), mapOf("Lib?" to "B, A"), listOf(q("Lib?", listOf("A, B", "B, A")))).isEmpty())
        assertFalse(Delegated.changedAnswers(mapOf("Which?" to "first A, then B"), mapOf("Which?" to "then B, first A"), multi).isEmpty())
        // The form sends only what changed.
        val qs = listOf(q("Driver?", listOf("pgx", "sqlc")), q("Test first?", listOf("Yes", "No")))
        val before = mapOf("Driver?" to "pgx", "Test first?" to "Yes")
        val form = ReanswerForm.start(qs, before)
        assertTrue(form.changes(qs, before).isEmpty())
        assertEquals(mapOf("Driver?" to "sqlc"), form.toggle(0, qs[0], "sqlc").changes(qs, before))
    }
}
