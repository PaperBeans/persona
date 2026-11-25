package persona.android.navigation

import androidx.navigation3.runtime.NavKey
import androidx.navigation3.scene.Scene
import kotlinx.serialization.Serializable
import persona.core.navigation.*

/** The existing Scheduled Tasks screen can be picked as a root from the merged sidebar. */
@Serializable
internal data object SidebarScheduleRoute : NavKey

/** Keep the logical tab anchors. The first Agent detail is the visible root unless it came from another page. */
internal fun mergedVisibleRoot(
    tab: AppTab,
    stack: List<NavKey>,
    compactPlace: (NavKey) -> Boolean = { false },
    agentDrillDown: (NavKey) -> Boolean = { false },
): NavKey = when (tab) {
    AppTab.Persona -> stack.getOrNull(1)?.takeIf { stack.firstOrNull() == AssistantRoute && compactPlace(it) }
        ?: stack.firstOrNull() ?: AssistantRoute
    AppTab.Agents -> if (stack.drop(1).any(agentDrillDown)) AgentsRoute else stack.getOrNull(1) ?: AgentsRoute
}

internal fun isMergedAgentPick(key: NavKey): Boolean = when (key) {
    is ChatRoute -> key.kind != ConversationKind.Assistant
    is SpaceRoute, is MachineSpacesRoute, is MachineDetailRoute, TerminalPanesRoute -> true
    else -> false
}

internal fun mergedAgentRootSwapped(from: Scene<NavKey>, to: Scene<NavKey>): Boolean {
    fun root(scene: Scene<NavKey>): Any? =
        (scene.previousEntries + scene.entries).distinctBy { it.contentKey }.getOrNull(1)?.contentKey
    return root(from) != root(to)
}

/** External opens use the router first, including the chat below a terminal, then reveal that target as the root. */
internal fun keepMergedAgentTarget(stack: MutableList<NavKey>, requested: NavKey) {
    val visible = stack.lastOrNull() ?: return
    when (requested) {
        is ChatRoute, is InteractionRoute -> replaceBackStack(stack, listOf(AgentsRoute, visible))
        is TerminalRoute -> replaceBackStack(stack, listOf(AgentsRoute) + stack.takeLast(2).filterNot { it == AgentsRoute })
    }
}

/**
 * Sidebar pick of an assistant conversation (Main Chat or another conversation row, #13165).
 * Returns true when the conversation host keeps a pushed drill-down in front, so the caller does not
 * re-request the conversation over it.
 */
internal fun selectSidebarConversation(
    useRootHost: Boolean,
    alreadyOnPersona: Boolean,
    sameConversation: Boolean,
    pageRoots: SidebarPageRoots,
    hosts: SidebarRootHosts,
    assistantStack: MutableList<NavKey>,
    showHost: (NavKey, List<NavKey>) -> Unit,
    assignPersona: (MutableList<NavKey>) -> Unit,
): Boolean {
    if (!useRootHost) {
        hosts.showConversation()
        while (assistantStack.size > 1) assistantStack.removeAt(assistantStack.lastIndex)
        assignPersona(assistantStack)
        return false
    }
    if (!alreadyOnPersona && sameConversation && pageRoots.current == AssistantRoute) {
        showHost(AssistantRoute, hosts.current.toList())
        assignPersona(hosts.current)
        return hosts.current.size > 1
    }
    val step = pageRoots.select(
        AssistantRoute,
        hosts.current.drop(1),
        contentChanged = !sameConversation,
    )
    showHost(step.root, step.stack)
    assignPersona(hosts.current)
    return sameConversation && step.path.isNotEmpty()
}
