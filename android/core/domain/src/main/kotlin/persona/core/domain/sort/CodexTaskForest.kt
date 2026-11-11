package persona.core.domain.sort

import persona.core.contracts.AgentState
import persona.core.contracts.Session

val Session.isCodexTask: Boolean get() = sourceKind == "codex_history" || sourceKind == "codex_app_server"
private val Session.hasCodexIdentity: Boolean get() = agent == "codex" || sourceKind?.startsWith("codex_") == true

/**
 * Another session started this one: a Codex subagent, or a runtime's session one of its agents created (a Paseo agent,
 * #4494), whichever agent runs it. Only Codex provenance and a runtime's delegation define this hierarchy; terminal pane
 * relationships keep their existing behavior.
 */
val Session.isSubtask: Boolean get() = (hasCodexIdentity || sourceKind == "runtime") &&
    (isSubagent || !parentSessionId.isNullOrBlank() || !parentThreadId.isNullOrBlank() ||
        (!rootSessionId.isNullOrBlank() && rootSessionId != sessionId))

/**
 * A subtask listed under its parent's "Subtasks (N)" and left out of every count. One that needs the user is not: it is a
 * row of its own, in Needs you and counted (docs/design/DESIGN_SYSTEM.md 3.4, 4.8) until it no longer needs them. One whose
 * parent the list does not draw is a row of its own too ([codexTaskForest]), still not counted.
 */
val AgentSession.isNestedSubtask: Boolean get() = session.isSubtask && state != AgentState.NeedsYou

data class CodexTaskForest(val roots: List<AgentSession>, val children: Map<String, List<AgentSession>>) {
    /** Search retains an ancestor when a descendant matches, so the matching conversation remains reachable. */
    fun searchSettings(settings: AgentListSettings): AgentListSettings {
        if (settings.query.isEmpty()) return settings
        fun matches(row: AgentSession): Boolean = settings.admits(row.session) || children[row.id].orEmpty().any(::matches)
        return settings.copy(queryAncestors = settings.queryAncestors + roots.filter(::matches).map { it.id })
    }
}

/**
 * Hangs each [isNestedSubtask] under its parent, on its own machine: the session its parent session id names, else (naming
 * none) the one Codex row of its source whose thread its parent thread id names, else the session its root session id
 * names. A session id can name a session of any source: a Codex thread's subagents sit under the runtime session that runs
 * the thread, a runtime's Codex agent under a Claude one.
 *
 * [runs] says which rows the list of running sessions draws, where the range has one. A subtask it draws hangs under its
 * parent only while it draws a row above the subtask too: a working agent whose creator was let go would be drawn
 * nowhere, so it is a row of its own (#4494). A subtask that list does not draw stays under its parent, as a Codex
 * thread's finished subagents do. The rows in [ownRows] hang under nothing: rows of their own the user opened under a
 * quick filter, which stay where they were while it is on (a subtask that needed the user and was answered, too).
 */
fun codexTaskForest(
    rows: List<AgentSession>, ownRows: Set<String> = emptySet(), runs: ((AgentSession) -> Boolean)? = null,
): CodexTaskForest {
    val bySession = rows.associateBy { it.session.machineId to it.session.sessionId }
    val byThread = rows.filter { it.session.hasCodexIdentity && !it.session.sourceThreadId.isNullOrBlank() }
        .groupBy { Triple(it.session.machineId, it.session.sourceInstanceId, it.session.sourceThreadId) }
    val parents = mutableMapOf<String, String>()
    rows.filter { it.isNestedSubtask && it.id !in ownRows }.forEach { row ->
        val s = row.session
        val parent = s.parentSessionId?.let { bySession[s.machineId to it] }
            ?: s.parentThreadId?.takeIf { s.parentSessionId.isNullOrBlank() }?.let { byThread[Triple(s.machineId, s.sourceInstanceId, it)]?.singleOrNull() }
            ?: s.rootSessionId?.let { bySession[s.machineId to it] }
        if (parent != null && parent.id != row.id) parents[row.id] = parent.id
    }
    // Corrupt cycles must not hide every member or recurse forever. Keep one deterministic orphan root.
    parents.keys.sorted().forEach { start ->
        val visited = mutableSetOf<String>()
        var current: String? = start
        while (current != null && visited.add(current)) current = parents[current]
        if (current != null) parents.remove(current)
    }
    if (runs != null) {
        val running = rows.filter(runs).mapTo(HashSet()) { it.id }
        parents.keys.filter { id -> id in running && generateSequence(parents[id]) { parents[it] }.none { it in running } }
            .forEach(parents::remove)
    }
    val children = rows.filter { it.id in parents }.groupBy { parents.getValue(it.id) }
        .mapValues { (_, members) -> members.sortedWith(compareByDescending<AgentSession> { it.updatedAt }.thenBy { it.id }) }
    return CodexTaskForest(rows.filterNot { it.id in parents }, children)
}
