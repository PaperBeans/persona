package persona.core.domain.sort

import persona.core.contracts.CoreText.Key
import persona.core.contracts.CoreText
import persona.core.contracts.AgentState
import persona.core.contracts.Session
import persona.core.contracts.WorkState
import persona.core.contracts.parseTime
import java.time.Duration
import java.time.Instant

// A session's work state as the lists show it (docs/assistant/agent-work.md; the web's lib/work.ts): the hub triages each stopped
// turn into done or blocked on something named, with one line for the row.

/** The hub waits it out: it wakes it (wake_at), or it wakes itself (its background tasks, a /loop). */
val WorkState.hubWaits: Boolean
    get() = phase == "blocked" && on != "user" && (wakeAt != null || on == "external" && wait.isNullOrBlank() && agent == null)

/**
 * What an Awaiting Reply row says (#5096): what the hub's triage says it waits on the user for (blocked on user), else what its
 * last reply asks; null when neither has words.
 */
val Session.replyLine: String? get() =
    work?.takeIf { it.phase == "blocked" && it.on == "user" }?.line?.trim()?.takeIf(String::isNotEmpty)
        ?: asks?.trim()?.takeIf(String::isNotEmpty)

/**
 * The row's line for a stopped session the hub triaged: "✓ Conclusion" when done, what it waits for when blocked (with when the
 * hub acts next); null while it works or before the hub triaged its turn. A turn that ended with a closeout
 * ([shownCloseout]) and that the hub gave no line says the closeout's own words: its last words would be the raw block.
 * A turn that is not finished (an empty phase with a line) keeps the hub's line as it is: the hub already words it.
 * Where the closeout stands on merged and deployed is the row's [closeoutFacts], not part of this line: a phone's row
 * cuts a long line short, and the end is the part that would go.
 */
fun Session.workLine(now: Instant): CoreText? = triagedLine(now) ?: shownCloseout?.line()

private fun Session.triagedLine(now: Instant): CoreText? {
    val w = work ?: return null
    val line = w.line?.takeIf(String::isNotBlank) ?: return null
    if (w.phase == "working" || agentState == AgentState.Working) return null
    if (w.phase == "done") return CoreText.of(Key.WorkLineDone, line)
    val wake = parseTime(w.wakeAt) ?: return CoreText.plain(line)
    val retry = w.on == "transient"
    val left = Duration.between(now, wake)
    val minutes = Math.round(left.seconds / 60.0)
    val whenText = when {
        left.seconds < 60 -> CoreText.of(if (retry) Key.RetryNow else Key.WakeNow)
        minutes < 60 -> CoreText.of(if (retry) Key.RetryInMinutes else Key.WakeInMinutes, minutes.toInt())
        minutes % 60 == 0L -> CoreText.of(if (retry) Key.RetryInHours else Key.WakeInHours, (minutes / 60).toInt())
        else -> CoreText.of(if (retry) Key.RetryInHoursMinutes else Key.WakeInHoursMinutes, minutes / 60, minutes % 60)
    }
    return CoreText.of(Key.Parenthesized, line, whenText)
}
