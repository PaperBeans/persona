package persona.core.domain.tasks

import java.time.Instant
import persona.core.contracts.AskOption
import persona.core.contracts.TodoAsk
import persona.core.contracts.parseTime

// A decision the owner can answer on its row (#3804, docs/assistant/todos.md "Decisions the owner can answer"; the web's
// DecisionRow and lib/todos.ts `askOptions`): its options with the recommended one first, the fixed answers that say
// "I can't answer this as asked", and what happens when nobody answers.

/**
 * The options of a decision that can be answered on its row: a decision with at least two of them, each with a label.
 * None for any other ask, and for one from a hub that predates them: those keep their one button.
 */
val TodoAsk.decisionOptions: List<AskOption>
    get() {
        if (outcome != "decide") return emptyList()
        val labelled = options.orEmpty().filter { it.label.isNotBlank() }
        return if (labelled.size >= 2) labelled else emptyList()
    }

/** The fixed answers of a decision, in the order every client lists them. */
enum class AskResponse(val code: String) {
    DecideYourself("decide_yourself"), NotWhatIAsked("not_what_i_asked"), ExplainFirst("explain_first")
}

/** The fixed answers this hub takes for the ask; none from a hub that does not know them. */
val TodoAsk.fixedResponses: List<AskResponse>
    get() = AskResponse.entries.filter { responses?.contains(it.code) == true }

/**
 * A step that cannot be undone, or that other people will see, takes two presses: the first arms the option
 * ("Confirm: <option>"), the second answers.
 */
val TodoAsk.confirmsOptions: Boolean get() = whyOwner == "irreversible" || whyOwner == "visible_to_others"

/** What happens when nobody answers a decision. */
sealed interface AskDefault {
    /** The recommendation is taken at [at]. */
    data class TakenAt(val at: Instant) : AskDefault

    /** It is never taken by default: it waits for the owner. */
    data object Never : AskDefault
}

/** The recommendation is taken at the deadline, or never; null when the hub said neither (or gave no readable deadline). */
val TodoAsk.fallback: AskDefault?
    get() = when (default) {
        "never" -> AskDefault.Never
        "taken" -> parseTime(deadline)?.let(AskDefault::TakenAt)
        else -> null
    }

/** What is to be decided: the decision's own sentence, else its verdict line (an older hub's ask). */
val TodoAsk.asked: String get() = question?.takeIf(String::isNotBlank) ?: verdict
