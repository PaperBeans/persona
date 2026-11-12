package persona.android.navigation

import androidx.navigation3.runtime.NavKey
import persona.core.navigation.*
import org.junit.Assert.assertEquals
import org.junit.Test

class AppNavigatorTest {
    private val stacks = AppTab.entries.associateWith { mutableListOf<NavKey>(tabRoot(it)) }
    private var selected = AppTab.Agents
    private val navigator = AppNavigator(stacks, { selected }, { selected = it })

    @Test fun secondarySettingsPagesReturnToTheOriginalTabAndPage() {
        val origin = ChatRoute("machine", "session")
        val settingsPages = listOf(SettingsRoute, SettingsPageRoute(SettingsPage.About), PairingRoute, ConnectionSettingsRoute)
        for (tab in AppTab.entries) {
            for (page in settingsPages) {
                stacks.values.forEach { it.subList(1, it.size).clear() }
                selected = tab
                stacks.getValue(tab).add(origin)
                navigator.navigate(page)
                assertEquals(tab, selected)
                val destination = if (page == ConnectionSettingsRoute) SettingsPageRoute(SettingsPage.Diagnostics) else page
                assertEquals(listOf(tabRoot(tab), origin, SettingsRoute) + if (destination == SettingsRoute) emptyList() else listOf(destination),
                    stacks.getValue(tab))
                navigator.back()
                if (page != SettingsRoute) {
                    assertEquals(SettingsRoute, stacks.getValue(tab).last())
                    navigator.back()
                }
                assertEquals(tab, selected)
                assertEquals(listOf(tabRoot(tab), origin), stacks.getValue(tab))
                assertEquals(listOf(tabRoot(AppTab.entries.first { it != tab })), stacks.getValue(AppTab.entries.first { it != tab }))
            }
        }
    }

    @Test fun contextualModelSettingsReturnDirectlyToTheAssistantTask() {
        selected = AppTab.Persona
        val origin = TodoDetailRoute("example-task")
        navigator.navigate(origin)
        navigator.navigate(ModelPurposeRoute("example-purpose", "example-manager"))
        assertEquals(listOf(AssistantRoute, origin, ModelPurposeRoute("example-purpose", "example-manager")), stacks.getValue(selected))
        navigator.back()
        assertEquals(origin, stacks.getValue(selected).last())
    }

    @Test fun inboxIsPushedOntoTheCurrentStackAndBackReturnsToTheOrigin() {
        val origin = SettingsPageRoute(SettingsPage.Hubs)
        navigator.navigate(origin)
        navigator.navigate(InboxRoute)
        assertEquals(AppTab.Agents, selected)
        assertEquals(listOf(AgentsRoute, SettingsRoute, origin, InboxRoute), stacks.getValue(selected))
        navigator.back()
        assertEquals(origin, stacks.getValue(selected).last())
    }

    @Test fun legacyLedgerNavigationAndRestoredStacksReturnToConnectors() {
        navigator.navigate(SettingsPageRoute(SettingsPage.Sent))
        assertEquals(listOf(AgentsRoute, SettingsRoute, SettingsPageRoute(SettingsPage.Connectors), SettingsPageRoute(SettingsPage.Sent)),
            stacks.getValue(AppTab.Agents))
        navigator.back()
        assertEquals(SettingsPageRoute(SettingsPage.Connectors), stacks.getValue(AppTab.Agents).last())
        navigator.back()
        assertEquals(listOf(AgentsRoute, SettingsRoute), stacks.getValue(AppTab.Agents))

        stacks.getValue(AppTab.Agents).add(SettingsPageRoute(SettingsPage.Sent))
        val restored = AppNavigator(stacks, { selected }, { selected = it })
        restored.back()
        assertEquals(SettingsPageRoute(SettingsPage.Connectors), stacks.getValue(AppTab.Agents).last())
    }

    @Test fun confirmedCloseRemovesAllMatchingDestinationsAcrossTabs() {
        val target = persona.core.contracts.SessionKey("machine", "closed")
        val chat = ChatRoute(target.machineId, target.sessionId)
        val other = ChatRoute("other-machine", target.sessionId)
        stacks.getValue(AppTab.Agents).addAll(listOf(chat, TerminalRoute(target.machineId, target.sessionId)))
        stacks.getValue(AppTab.Persona).addAll(listOf(chat.copy(kind = ConversationKind.Assistant), InteractionRoute(chat, "request"), other))
        navigator.sessionClosed(target)
        assertEquals(listOf(AgentsRoute), stacks.getValue(AppTab.Agents))
        assertEquals(listOf(AssistantRoute, other), stacks.getValue(AppTab.Persona))
        navigator.sessionClosed(target)
        navigator.back()
        assertEquals(listOf(AgentsRoute), stacks.getValue(AppTab.Agents))
    }

    @Test fun aChatWhoseGroupWentAwayLeavesItsOwnTabNotTheOneOnScreen() {
        val chat = ChatRoute("machine~g0a1b2c3d", "s")
        val hubs = SettingsPageRoute(SettingsPage.Hubs)
        stacks.getValue(AppTab.Agents).add(chat)
        selected = AppTab.Persona
        navigator.navigate(hubs)
        navigator.groupGone(chat, null)
        assertEquals(AppTab.Persona, selected)
        assertEquals(listOf(AssistantRoute, SettingsRoute, hubs), stacks.getValue(AppTab.Persona))
        assertEquals(listOf(AgentsRoute), stacks.getValue(AppTab.Agents))

        val moved = ChatRoute("machine", "s")
        stacks.getValue(AppTab.Agents).add(InteractionRoute(chat, "request"))
        navigator.groupGone(chat, moved)
        assertEquals(AppTab.Persona, selected)
        assertEquals(listOf(AssistantRoute, SettingsRoute, hubs), stacks.getValue(AppTab.Persona))
        assertEquals(listOf(AgentsRoute, moved), stacks.getValue(AppTab.Agents))

        val router = agentsOwnAgentChats()
        stacks.getValue(AppTab.Agents).remove(moved)
        router.navigate(chat)
        router.groupGone(chat, moved)
        assertEquals(listOf(AgentsRoute, moved), stacks.getValue(AppTab.Agents))
        router.back()
        assertEquals(AppTab.Persona, selected)
        assertEquals(hubs, stacks.getValue(selected).last())
    }

    @Test fun switchingTabsReturnsToTheAgentsListAndBackNeverRemovesTheRoot() {
        val chat = ChatRoute("machine", "session")
        navigator.navigate(chat)
        navigator.selectTab(AppTab.Persona)
        navigator.back()
        assertEquals(listOf(AssistantRoute), stacks.getValue(AppTab.Persona))
        navigator.selectTab(AppTab.Agents)
        assertEquals(listOf(AgentsRoute), stacks.getValue(AppTab.Agents))
        navigator.back()
        navigator.back()
        assertEquals(listOf(AgentsRoute), stacks.getValue(AppTab.Agents))
    }

    @Test fun knownAgentChatFromSettingsReturnsToThatSettingsPage() {
        selected = AppTab.Persona
        val settings = SettingsPageRoute(SettingsPage.Notifications)
        navigator.navigate(settings)
        val chat = ChatRoute("machine", "session", ConversationKind.Agent)
        navigator.navigate(chat)
        navigator.navigate(chat)
        assertEquals(AppTab.Agents, selected)
        assertEquals(listOf(AgentsRoute, chat), stacks.getValue(AppTab.Agents))
        navigator.back()
        assertEquals(AppTab.Persona, selected)
        assertEquals(settings, stacks.getValue(selected).last())
    }

    @Test fun assistantChatsStayInPersonaAndLateMetadataRepairsRestoredChats() {
        var assistantKnown = false
        val assistantChat = ChatRoute("machine", "assistant")
        val router = AppNavigator(stacks, { selected }, { selected = it }, chatTab = {
            if (assistantKnown && it == assistantChat) AppTab.Persona else null
        })
        router.selectTab(AppTab.Persona)
        router.navigate(assistantChat)
        assertEquals(AppTab.Persona, selected)
        assertEquals(listOf(AgentsRoute), stacks.getValue(AppTab.Agents))
        router.back()

        stacks.getValue(AppTab.Agents).add(assistantChat)
        selected = AppTab.Agents // Restored position, not an explicit request to open the Agents list.
        assistantKnown = true
        router.restoreChatTabs()
        assertEquals(AppTab.Persona, selected)
        assertEquals(listOf(AssistantRoute, assistantChat), stacks.getValue(AppTab.Persona))
        assertEquals(listOf(AgentsRoute), stacks.getValue(AppTab.Agents))
        router.selectTab(AppTab.Agents)
        router.navigate(assistantChat)
        assertEquals(AppTab.Persona, selected)
        assertEquals(listOf(AssistantRoute, assistantChat), stacks.getValue(AppTab.Persona))
    }

    @Test fun restoredStackKeepsChatOwnership() {
        val chat = ChatRoute("machine", "assistant")
        stacks.getValue(AppTab.Persona).add(chat)
        navigator.navigate(chat)
        assertEquals(AppTab.Persona, selected)
        assertEquals(listOf(AssistantRoute, chat), stacks.getValue(AppTab.Persona))
    }

    @Test fun embeddedAssistantConversationsStayInThePersonaHome() {
        val assistantChat = ChatRoute("machine", "assistant")
        val opened = mutableListOf<Pair<ChatRoute, Boolean>>()
        val router = AppNavigator(stacks, { selected }, { selected = it }, chatTab = {
            if (it.sessionId == "assistant") AppTab.Persona else AppTab.Agents
        }, openAssistant = { chat, select ->
            (chat.sessionId == "assistant").also { if (it) { opened += chat to select; if (select) selected = AppTab.Persona } }
        })
        router.navigate(assistantChat)
        assertEquals(listOf(assistantChat to true), opened)
        assertEquals(AppTab.Persona, selected)
        assertEquals(listOf(AssistantRoute), stacks.getValue(AppTab.Persona))

        stacks.getValue(AppTab.Agents).add(assistantChat)
        router.navigate(InboxRoute)
        router.restoreChatTabs()
        assertEquals(assistantChat to false, opened.last())
        assertEquals(AppTab.Persona, selected)
        assertEquals(InboxRoute, stacks.getValue(selected).last())
        assertEquals(listOf(AgentsRoute), stacks.getValue(AppTab.Agents))

        val agentChat = ChatRoute("machine", "agent")
        router.navigate(agentChat)
        assertEquals(listOf(AgentsRoute, agentChat), stacks.getValue(AppTab.Agents))
    }

    private fun agentsOwnAgentChats() = AppNavigator(stacks, { selected }, { selected = it }, chatTab = { AppTab.Agents })

    @Test fun backFromAnAgentChatOpenedInThePersonaHomeReturnsToTheHome() {
        val router = agentsOwnAgentChats()
        selected = AppTab.Persona
        val chat = ChatRoute("machine", "agent")
        router.navigate(chat)
        assertEquals(AppTab.Agents, selected)
        assertEquals(listOf(AgentsRoute, chat), stacks.getValue(AppTab.Agents))
        router.back()
        assertEquals(AppTab.Persona, selected)
        assertEquals(listOf(AgentsRoute), stacks.getValue(AppTab.Agents))
        assertEquals(listOf(AssistantRoute), stacks.getValue(AppTab.Persona))

        stacks.getValue(AppTab.Agents).add(chat)
        router.navigate(chat)
        assertEquals(listOf(AgentsRoute, chat), stacks.getValue(AppTab.Agents))
        router.back()
        assertEquals(AppTab.Persona, selected)

        val known = ChatRoute("machine", "known", ConversationKind.Agent)
        router.navigate(known)
        assertEquals(AppTab.Agents, selected)
        router.back()
        assertEquals(AppTab.Persona, selected)
    }

    @Test fun pagesPushedAboveACrossTabChatGoBackToItFirst() {
        val router = agentsOwnAgentChats()
        selected = AppTab.Persona
        router.navigate(InboxRoute)
        val chat = ChatRoute("machine", "agent")
        val terminal = TerminalRoute("machine", "agent")
        router.navigate(chat)
        router.navigate(terminal)
        assertEquals(listOf(AgentsRoute, chat, terminal), stacks.getValue(AppTab.Agents))
        router.back()
        assertEquals(AppTab.Agents, selected)
        assertEquals(listOf(AgentsRoute, chat), stacks.getValue(AppTab.Agents))
        router.back()
        assertEquals(AppTab.Persona, selected)
        assertEquals(InboxRoute, stacks.getValue(selected).last())
    }

    @Test fun pickingATabForgetsWhereACrossTabPageCameFrom() {
        val router = agentsOwnAgentChats()
        selected = AppTab.Persona
        val chat = ChatRoute("machine", "agent")
        router.navigate(chat)
        router.selectTab(AppTab.Persona)
        router.selectTab(AppTab.Agents)
        router.back()
        assertEquals(AppTab.Agents, selected)
        assertEquals(listOf(AgentsRoute), stacks.getValue(AppTab.Agents))
        router.navigate(chat)
        router.back()
        assertEquals(AppTab.Agents, selected)
    }
}
