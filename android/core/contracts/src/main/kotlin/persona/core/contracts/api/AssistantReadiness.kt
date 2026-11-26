package persona.core.contracts.api

import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.flowOf
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/** The hub feature behind GET /api/v1/assistant/readiness (docs/protocol/protocol.md "Main chat readiness"). */
const val AssistantReadinessFeature = "assistant_readiness"

/**
 * What the main conversation still needs (docs/assistant/onboarding.md 4.4, 5.1): the hub works it out, every client draws the same
 * thing from it. All fields are optional so a newer hub's additions never break reading it.
 */
@Serializable
data class AssistantReadiness(
    val version: Int = 1,
    /** `full` / `setup` / `chat_only` (P1). */
    val mode: String = FULL,
    val ready: Boolean = true,
    /** The first step that is not ok or skipped; null when there is none. */
    val next: String? = null,
    val machine: ReadinessMachine? = null,
    val launcher: String = "",
    val steps: List<ReadinessStep> = emptyList(),
    val suggestions: List<ReadinessSuggestion> = emptyList(),
    /** Only on `?probe=1` with the computer ok: the one-word check's result. */
    val probe: ReadinessProbe? = null,
    val dismissed: Boolean = false,
    @SerialName("checked_at") val checkedAt: String? = null,
) {
    fun step(id: String): ReadinessStep? = steps.firstOrNull { it.id == id }

    /**
     * The step the card over the composer shows and the send button names: in `setup`, the first of computer / Claude
     * Code / model that is to do or a problem. A rate limit passes by itself and never blocks; "Try a message" never blocks; a step
     * a newer hub adds counts through [next] (docs/assistant/onboarding.md 5 "General rules"). Null when nothing blocks sending.
     */
    fun blockingStep(): ReadinessStep? {
        if (mode != SETUP) return null
        for (id in Blocking) {
            val step = step(id) ?: continue
            if (step.state !in setOf(ReadinessStep.TODO, ReadinessStep.PROBLEM)) continue
            if (step.code == "rate_limited") continue
            return step
        }
        val next = next ?: return null
        if (next in Blocking || next == ReadinessStep.FIRST_MESSAGE) return null
        return step(next)?.takeIf { it.state != ReadinessStep.OK && it.state != ReadinessStep.SKIPPED }
    }

    companion object {
        const val FULL = "full"
        const val SETUP = "setup"
        const val CHAT_ONLY = "chat_only"
        /** The steps that decide the mode, in the hub's order. */
        val Blocking = listOf(ReadinessStep.COMPUTER, ReadinessStep.CLAUDE, ReadinessStep.MODEL)
    }
}

@Serializable
data class ReadinessMachine(val id: String = "", val name: String = "")

@Serializable
data class ReadinessStep(
    /** `computer`, `claude`, `model`, `first_message`, in that order. */
    val id: String = "",
    /** `ok` / `todo` / `problem` / `checking` / `unknown` / `skipped`. */
    val state: String = UNKNOWN,
    val code: String? = null,
    val machine: String? = null,
    val launcher: String? = null,
    val version: String? = null,
    /** The hub's words, in the request's language. */
    val title: String? = null,
    val detail: String? = null,
    val action: ReadinessAction? = null,
    @SerialName("alt_actions") val altActions: List<ReadinessAction> = emptyList(),
) {
    companion object {
        const val COMPUTER = "computer"
        const val CLAUDE = "claude"
        const val MODEL = "model"
        const val FIRST_MESSAGE = "first_message"
        const val OK = "ok"
        const val TODO = "todo"
        const val PROBLEM = "problem"
        const val CHECKING = "checking"
        const val UNKNOWN = "unknown"
        const val SKIPPED = "skipped"
    }
}

@Serializable
data class ReadinessAction(val id: String = "")

@Serializable
data class ReadinessSuggestion(
    val id: String = "",
    val title: String = "",
    val prompt: String? = null,
    /** Opens local help; never sent to the model. */
    val local: Boolean = false,
    val needs: List<String> = emptyList(),
)

@Serializable
data class ReadinessProbe(
    val ok: Boolean = false,
    val agent: String? = null,
    val model: String? = null,
    val answer: String? = null,
    val millis: Long? = null,
    val error: String? = null,
)

/**
 * The selected hub's readiness for pages outside the assistant tab (Settings › Setup checklist). [available] follows the hub's
 * features; an old hub never offers it and the pages stay as they were.
 */
interface AssistantReadinessSource {
    val available: Flow<Boolean> get() = flowOf(false)
    suspend fun readiness(probe: Boolean = false): AssistantReadiness
    /** "Later" (true) or undoing it (false): POST /api/v1/assistant/readiness/dismiss. */
    suspend fun dismiss(dismissed: Boolean)
}

/**
 * A call the hub answered with a status and an error code (core/hub's `HubFailure`), for layers that do not see the
 * transport: the outbox tells a refused main-conversation start (409 `no_machine`…) from a network failure by it.
 */
interface HubCodedFailure {
    val status: Int?
    val code: String?
    val delivered: String? get() = null
}

/** The first [HubCodedFailure] in [error]'s causes. */
fun hubCodedFailure(error: Throwable): HubCodedFailure? =
    generateSequence(error) { it.cause }.take(16).filterIsInstance<HubCodedFailure>().firstOrNull()
