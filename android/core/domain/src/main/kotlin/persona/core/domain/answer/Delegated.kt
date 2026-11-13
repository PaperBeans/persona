package persona.core.domain.answer

import persona.core.contracts.DelegatedAnswer
import persona.core.contracts.InteractionRecord
import persona.core.contracts.Question
import persona.core.contracts.parseTime
import java.time.Instant

/**
 * The questions the assistant answered for the user (iOS `Delegated`, the web's lib/delegate.ts; docs/assistant/assistant.md
 * "Assistant answers questions first"): which ones the history lists, what the answers are now, Change Answer's starting
 * point and what of its form goes to the Agent. The data layer keeps them (`InteractionCenter.delegated`).
 */
object Delegated {
    /** A multi-select answer is its labels joined so, as the apps send it. */
    const val PICKS_SEPARATOR = ", "
    /** Answered questions kept for their chats at most, the oldest put away first. */
    const val KEPT = 50

    /** The questions the history says the assistant answered, newest first. */
    fun history(records: List<InteractionRecord>): List<DelegatedAnswer> =
        records.filter { it.status == "resolved" && it.interaction.kind == "question" && it.resolution?.answeredBy == DelegatedAnswer.ASSISTANT }
            .map { DelegatedAnswer(it.interaction, it.resolution?.answers.orEmpty(), it.resolution?.reason.orEmpty(), parseTime(it.resolvedAt)) }
            .sortedByDescending { it.at ?: Instant.EPOCH }

    /** What the answers are now: the assistant's, with what the user changed since. */
    fun latest(answer: DelegatedAnswer, changed: Map<String, String>?): Map<String, String> = answer.answers + changed.orEmpty()

    /**
     * The option labels [answer] picks on [question]: the label itself, or for a multi-select every one of its ", "-joined
     * labels; empty for no answer, null when it is words of its own (or names something that is no option).
     */
    fun labels(answer: String, question: Question): List<String>? {
        val a = answer.trim()
        if (a.isEmpty()) return emptyList()
        val labels = question.options.map { it.label }
        if (a in labels) return listOf(a)
        if (!question.multiSelect) return null
        val parts = a.split(PICKS_SEPARATOR).map { it.trim() }.filter(String::isNotEmpty)
        return parts.takeIf { it.isNotEmpty() && it.all(labels::contains) }
    }

    /** Whether two answers to [question] say the same: the same words, or a multi-select's same options in any order. */
    fun sameAnswer(question: Question?, a: String, b: String): Boolean {
        val x = a.trim()
        val y = b.trim()
        if (x == y) return true
        if (question == null || !question.multiSelect) return false
        val l = labels(x, question)?.takeIf(List<String>::isNotEmpty) ?: return false
        val r = labels(y, question)?.takeIf(List<String>::isNotEmpty) ?: return false
        return l.sorted() == r.sorted()
    }

    /**
     * The answers in [after] that say something other than [before]: only those go to the Agent (an unchanged one would
     * only repeat what it already went on with); an emptied answer is no answer.
     */
    fun changedAnswers(before: Map<String, String>, after: Map<String, String>, questions: List<Question> = emptyList()): Map<String, String> =
        after.mapValues { it.value.trim() }.filter { (question, now) ->
            now.isNotEmpty() && !sameAnswer(questions.firstOrNull { it.question == question }, now, before[question].orEmpty())
        }
}

/**
 * Change Answer's form (iOS `ReanswerSheet`): per question (by its index) the options picked and the words typed in
 * Other answer. Picking clears the words, typing clears the picks.
 */
data class ReanswerForm(val picked: Map<Int, List<String>> = emptyMap(), val custom: Map<Int, String> = emptyMap()) {
    /** A multi-select toggles the option; a single choice picks it, or unpicks it when it was the one. */
    fun toggle(index: Int, question: Question, label: String): ReanswerForm {
        val was = picked[index].orEmpty()
        val now = if (question.multiSelect) (if (label in was) was - label else was + label) else if (was == listOf(label)) emptyList() else listOf(label)
        return ReanswerForm(picked + (index to now), if (now.isEmpty()) custom else custom + (index to ""))
    }

    fun typed(index: Int, text: String): ReanswerForm =
        ReanswerForm(if (text.isBlank()) picked else picked + (index to emptyList()), custom + (index to text))

    /** The answers by question text, as a card sends them: the words typed, else the picks joined by ", ". */
    fun answers(questions: List<Question>): Map<String, String> = questions.withIndex().associate { (i, q) ->
        val typed = custom[i].orEmpty().trim()
        q.question to typed.ifEmpty { picked[i].orEmpty().joinToString(Delegated.PICKS_SEPARATOR) }
    }

    /** Only what says something other than [before] (the answers as they are now). */
    fun changes(questions: List<Question>, before: Map<String, String>): Map<String, String> =
        Delegated.changedAnswers(before, answers(questions), questions)

    companion object {
        /** The answers so far: picked where they are options, typed out where not. */
        fun start(questions: List<Question>, answers: Map<String, String>): ReanswerForm {
            val picked = mutableMapOf<Int, List<String>>()
            val custom = mutableMapOf<Int, String>()
            questions.forEachIndexed { i, q ->
                val a = answers[q.question].orEmpty().trim()
                val labels = Delegated.labels(a, q)
                picked[i] = labels.orEmpty()
                if (labels == null && a.isNotEmpty()) custom[i] = a
            }
            return ReanswerForm(picked, custom)
        }
    }
}
