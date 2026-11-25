package persona.android.navigation

import androidx.compose.runtime.mutableStateListOf
import androidx.navigation3.runtime.NavKey
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Test
import persona.core.navigation.*
import persona.feature.assistant.AssistantSidebarShade
import persona.feature.inbox.InboxLanding

class MergedSidebarNavigationTest {
    private val assistant = mutableListOf<NavKey>(AssistantRoute)
    private val firstAgent = ChatRoute("computer", "first", ConversationKind.Agent)
    private val secondAgent = ChatRoute("computer", "second", ConversationKind.Agent)
    private val agents = mutableListOf<NavKey>(AgentsRoute, firstAgent)
    private val stacks = mutableMapOf(AppTab.Persona to assistant, AppTab.Agents to agents)
    private var tab = AppTab.Agents
    private val navigator = AppNavigator(stacks, { tab }, { tab = it }, chatTab = { AppTab.Agents })

    @Test fun agentPickReplacesRootAndDrillDownBackKeepsTheSameRoute() {
        navigator.pickSidebarAgent(ChatRoute(firstAgent.machineId, firstAgent.sessionId))
        assertSame(firstAgent, agents.last())
        navigator.pickSidebarAgent(secondAgent)
        assertEquals(listOf(AgentsRoute, secondAgent), agents)
        navigator.navigate(TerminalRoute(secondAgent.machineId, secondAgent.sessionId))
        assertEquals(secondAgent, mergedVisibleRoot(tab, agents))
        navigator.back()
        assertSame(secondAgent, agents.last())
        navigator.back()
        assertEquals(listOf(AgentsRoute), agents)
    }

    @Test fun notificationAndTerminalUseTheExistingKeysAndReturnPaths() {
        val request = InteractionRoute(secondAgent, "pending")
        ResolvedRoute(AppTab.Agents, request).open(stacks)
        keepMergedAgentTarget(agents, request)
        assertEquals(listOf(AgentsRoute, request), agents)
        assertSame(request, mergedVisibleRoot(tab, agents))
        val terminal = TerminalRoute(secondAgent.machineId, secondAgent.sessionId)
        ResolvedRoute(AppTab.Agents, terminal).open(stacks)
        keepMergedAgentTarget(agents, terminal)
        assertEquals(listOf(AgentsRoute, request, terminal), agents)
    }

    @Test fun anExternalTerminalFromTheAgentsListKeepsOneAnchor() {
        val terminal = TerminalRoute("computer", "pane:example")
        agents[1] = terminal
        keepMergedAgentTarget(agents, terminal)
        assertEquals(listOf(AgentsRoute, terminal), agents)
        navigator.back()
        assertEquals(listOf(AgentsRoute), agents)
    }

    @Test fun compactMergedPlaceReplacesTheRootAndSeparateRetainsItsPush() {
        val hosts = SidebarRootHosts(assistant)
        val roots = SidebarPageRoots()
        val landing = InboxLanding()
        fun pick(useRootHost: Boolean) {
            applySidebarDestination(TodosRoute, useRootHost, roots, hosts, assistant, hosts::activate,
                { stacks[AppTab.Persona] = it }, landing, emptyList())
        }
        pick(useRootHost = false)
        assertEquals(listOf(AssistantRoute, TodosRoute), assistant)
        pick(useRootHost = true)
        assertEquals(listOf(TodosRoute), hosts.current)
        hosts.current.add(TodoDetailRoute("trip"))
        assertEquals(AssistantSidebarShade.Todos, sidebarShadeKey(mergedVisibleRoot(AppTab.Persona, hosts.current)))
        tab = AppTab.Persona
        navigator.back()
        assertEquals(listOf(TodosRoute), hosts.current)
    }

    @Test fun anAgentOpenedInsideAnotherPageKeepsTheSourceForBack() {
        tab = AppTab.Persona
        navigator.navigate(secondAgent)
        assertEquals(AppTab.Persona, navigator.sourceTab(secondAgent))
        assertSame(AgentsRoute, mergedVisibleRoot(tab, agents, agentDrillDown = { navigator.sourceTab(it) == AppTab.Persona }))
        navigator.back()
        assertEquals(AppTab.Persona, tab)
        assertSame(AssistantRoute, assistant.last())
    }

    @Test fun conversationOpenedAgentStaysOnMainChatStackAcrossSidebarAgentSwitch() {
        val conversation = mutableStateListOf<NavKey>(AssistantRoute)
        val hosts = SidebarRootHosts(conversation)
        val roots = SidebarPageRoots()
        val agentStack = mutableStateListOf<NavKey>(AgentsRoute)
        val mergedStacks = mutableMapOf<AppTab, MutableList<NavKey>>(
            AppTab.Persona to hosts.current,
            AppTab.Agents to agentStack,
        )
        var selected = AppTab.Persona
        val mergedNav = AppNavigator(
            mergedStacks, { selected }, { selected = it },
            chatTab = { AppTab.Agents },
            keepPersonaAgents = { true },
        )
        fun showHost(root: NavKey, next: List<NavKey>) {
            hosts.activate(root, next)
            mergedStacks[AppTab.Persona] = hosts.current
        }

        // Open an Agent from inside the main conversation (e.g. a waiting card or Brief card).
        mergedNav.navigate(firstAgent)
        assertEquals(AppTab.Persona, selected)
        assertEquals(listOf(AssistantRoute, firstAgent), conversation.toList())
        assertEquals(listOf(AgentsRoute), agentStack.toList())
        assertEquals(AssistantRoute, mergedVisibleRoot(selected, hosts.current))
        assertEquals(AssistantSidebarShade.Conversation, sidebarShadeKey(mergedVisibleRoot(selected, hosts.current)))

        // Late session snapshots running restoreChatTabs must leave the drill-down on the conversation stack.
        mergedNav.restoreChatTabs()
        assertEquals(AppTab.Persona, selected)
        assertEquals(listOf(AssistantRoute, firstAgent), conversation.toList())
        assertEquals(listOf(AgentsRoute), agentStack.toList())

        // Pick another Agent in the sidebar: Agents switches cleanly, conversation keeps its pushed Agent.
        mergedNav.pickSidebarAgent(secondAgent)
        assertEquals(AppTab.Agents, selected)
        assertEquals(listOf(AgentsRoute, secondAgent), agentStack.toList())
        assertEquals(listOf(AssistantRoute, firstAgent), conversation.toList())

        // Pick Main Chat in the sidebar again: restores the pushed Agent in front.
        val kept = selectSidebarConversation(
            useRootHost = true,
            alreadyOnPersona = selected == AppTab.Persona,
            sameConversation = true,
            pageRoots = roots,
            hosts = hosts,
            assistantStack = conversation,
            showHost = ::showHost,
            assignPersona = { mergedStacks[AppTab.Persona] = it },
        )
        selected = AppTab.Persona
        assertTrue(kept)
        assertEquals(listOf(AssistantRoute, firstAgent), hosts.current.toList())
        assertEquals(AssistantSidebarShade.Conversation, sidebarShadeKey(mergedVisibleRoot(selected, hosts.current)))

        // Back returns from the pushed Agent to the main conversation.
        mergedNav.back()
        assertEquals(AppTab.Persona, selected)
        assertEquals(listOf(AssistantRoute), hosts.current.toList())
    }

    @Test fun repickingMainChatWhileInFrontPopsAndAnotherConversationStartsAtRoot() {
        val conversation = mutableStateListOf<NavKey>(AssistantRoute)
        val hosts = SidebarRootHosts(conversation)
        val roots = SidebarPageRoots()
        val agentStack = mutableStateListOf<NavKey>(AgentsRoute)
        val mergedStacks = mutableMapOf<AppTab, MutableList<NavKey>>(
            AppTab.Persona to hosts.current,
            AppTab.Agents to agentStack,
        )
        var selected = AppTab.Persona
        val mergedNav = AppNavigator(
            mergedStacks, { selected }, { selected = it },
            chatTab = { AppTab.Agents },
            keepPersonaAgents = { true },
        )
        fun showHost(root: NavKey, next: List<NavKey>) {
            hosts.activate(root, next)
            mergedStacks[AppTab.Persona] = hosts.current
        }

        // Re-picking Main Chat while already in front pops to the bare conversation (tab-bar rule).
        mergedNav.navigate(firstAgent)
        val keptOnRepick = selectSidebarConversation(
            useRootHost = true,
            alreadyOnPersona = true,
            sameConversation = true,
            pageRoots = roots,
            hosts = hosts,
            assistantStack = conversation,
            showHost = ::showHost,
            assignPersona = { mergedStacks[AppTab.Persona] = it },
        )
        assertFalse(keptOnRepick)
        assertEquals(listOf(AssistantRoute), hosts.current.toList())

        // Picking a different assistant conversation from another Agent drops Main Chat's pushed Agent.
        mergedNav.navigate(firstAgent)
        mergedNav.pickSidebarAgent(secondAgent)
        assertEquals(AppTab.Agents, selected)
        val keptForOtherChat = selectSidebarConversation(
            useRootHost = true,
            alreadyOnPersona = false,
            sameConversation = false,
            pageRoots = roots,
            hosts = hosts,
            assistantStack = conversation,
            showHost = ::showHost,
            assignPersona = { mergedStacks[AppTab.Persona] = it },
        )
        selected = AppTab.Persona
        assertFalse(keptForOtherChat)
        assertEquals(listOf(AssistantRoute), hosts.current.toList())
    }

    @Test fun pickingTheSameAgentInSidebarPromotesItsInteractionDrillDownToRoot() {
        val conversation = mutableStateListOf<NavKey>(AssistantRoute)
        val agentStack = mutableStateListOf<NavKey>(AgentsRoute)
        val mergedStacks = mutableMapOf<AppTab, MutableList<NavKey>>(
            AppTab.Persona to conversation,
            AppTab.Agents to agentStack,
        )
        var selected = AppTab.Persona
        val mergedNav = AppNavigator(
            mergedStacks, { selected }, { selected = it },
            chatTab = { AppTab.Agents },
            keepPersonaAgents = { true },
        )
        val interaction = InteractionRoute(firstAgent, "req-1")
        mergedNav.navigate(interaction)
        assertEquals(AppTab.Persona, selected)
        assertEquals(listOf(AssistantRoute, interaction), conversation.toList())

        mergedNav.pickSidebarAgent(firstAgent)
        assertEquals(AppTab.Agents, selected)
        assertEquals(listOf(AgentsRoute, firstAgent), agentStack.toList())
        assertEquals(listOf(AssistantRoute), conversation.toList())
    }

    @Test fun sidebarPlacesAndMainChatBothKeepTheirOpenedAgentsAcrossSidebarAgentSwitches() {
        val conversation = mutableStateListOf<NavKey>(AssistantRoute)
        val hosts = SidebarRootHosts(conversation)
        val roots = SidebarPageRoots()
        val landing = InboxLanding()
        val agentStack = mutableStateListOf<NavKey>(AgentsRoute)
        val mergedStacks = mutableMapOf<AppTab, MutableList<NavKey>>(
            AppTab.Persona to hosts.current,
            AppTab.Agents to agentStack,
        )
        var selected = AppTab.Persona
        val mergedNav = AppNavigator(
            mergedStacks, { selected }, { selected = it },
            chatTab = { AppTab.Agents },
            keepPersonaAgents = { true },
        )
        fun showHost(root: NavKey, next: List<NavKey>) {
            hosts.activate(root, next)
            mergedStacks[AppTab.Persona] = hosts.current
        }
        val thirdAgent = ChatRoute("computer", "third", ConversationKind.Agent)

        // Main Chat pushes firstAgent.
        mergedNav.navigate(firstAgent)
        assertEquals(listOf(AssistantRoute, firstAgent), hosts.current.toList())

        // Switch to To-dos, open a to-do and its Agent (thirdAgent).
        applySidebarDestination(TodosRoute, true, roots, hosts, conversation, ::showHost,
            { mergedStacks[AppTab.Persona] = it }, landing, emptyList(), alreadyInFront = true)
        mergedNav.navigate(TodoDetailRoute("trip"))
        mergedNav.navigate(thirdAgent)
        assertEquals(listOf(TodosRoute, TodoDetailRoute("trip"), thirdAgent), hosts.current.toList())

        // Pick secondAgent in the sidebar.
        mergedNav.pickSidebarAgent(secondAgent)
        assertEquals(AppTab.Agents, selected)

        // Returning to To-dos from Agents must restore its stack, not pop it.
        applySidebarDestination(TodosRoute, true, roots, hosts, conversation, ::showHost,
            { mergedStacks[AppTab.Persona] = it }, landing, emptyList(), alreadyInFront = false)
        selected = AppTab.Persona
        assertEquals(listOf(TodosRoute, TodoDetailRoute("trip"), thirdAgent), hosts.current.toList())

        // Pick Main Chat: restores firstAgent, and Back returns to Main Chat.
        val kept = selectSidebarConversation(
            useRootHost = true,
            alreadyOnPersona = true,
            sameConversation = true,
            pageRoots = roots,
            hosts = hosts,
            assistantStack = conversation,
            showHost = ::showHost,
            assignPersona = { mergedStacks[AppTab.Persona] = it },
        )
        assertTrue(kept)
        assertEquals(listOf(AssistantRoute, firstAgent), hosts.current.toList())
        mergedNav.back()
        assertEquals(listOf(AssistantRoute), hosts.current.toList())
    }
}
