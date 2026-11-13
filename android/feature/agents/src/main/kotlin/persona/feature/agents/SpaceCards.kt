package persona.feature.agents

import persona.core.domain.sort.AgentSession
import persona.core.domain.sort.AgentTab
import persona.core.domain.sort.agentTabs
import persona.core.domain.sort.tabKey

// What the space page's cards show (iOS `SpaceView`, `SlotCard`, `TabPanes`; #4338 A11), apart from how they look.

/**
 * The space page's cards in iOS's order: the most urgent first (needs you, unread, working, seen: `AgentState.priority`),
 * then by tab and session id; the agents of one tab together where the first of them is, in the order of their panes
 * (iOS `TabPanes.grouped`).
 */
internal fun spaceCardOrder(members: List<AgentSession>): List<AgentSession> {
    val urgent = members.sortedWith(compareBy<AgentSession> { it.state.priority }
        .thenBy { it.session.slotIndex ?: 0 }.thenBy { it.session.sessionId })
    val tabs = agentTabs(urgent)
    val placed = mutableSetOf<String>()
    return urgent.flatMap { row ->
        val key = row.session.tabKey
        val tab = key?.let(tabs::get)
        when {
            tab == null -> listOf(row)
            placed.add(key) -> tab.members
            else -> emptyList()
        }
    }
}

/**
 * [path] with the home folder written as ~ (iOS `Fmt.path`): the computer's [home] when the path is in it, else the
 * usual homes (`/Users/<name>`, `/home/<name>`, `/data00/home/<name>`). Unlike iOS, a home only matches a whole folder:
 * `/home/al` does not shorten `/home/alex`.
 */
internal fun shortPath(path: String, home: String?): String {
    val root = home?.trimEnd('/').orEmpty()
    if (root.isNotEmpty() && (path == root || path.startsWith("$root/"))) return "~" + path.removePrefix(root)
    for (prefix in HomeRoots) if (path.startsWith(prefix)) {
        val rest = path.removePrefix(prefix)
        val slash = rest.indexOf('/')
        return if (slash >= 0) "~" + rest.substring(slash) else "~"
    }
    return path
}

private val HomeRoots = listOf("/Users/", "/home/", "/data00/home/")

/**
 * What an agent is called beside its tab mates (iOS `TabPanes.paneName`): its agent ("Claude", "Codex"); two of the
 * others running the same agent are numbered in pane order ("Claude 2"). The starter is told apart by its mark, so it
 * is not numbered.
 */
internal fun AgentTab.paneName(row: AgentSession): String {
    val label = row.session.agentLabel
    val first = launcher
    if (row.id == first?.id) return label
    val same = members.filter { it.id != first?.id && it.session.agentLabel == label }
    val index = same.indexOfFirst { it.id == row.id }
    return if (same.size > 1 && index >= 0) "$label ${index + 1}" else label
}
