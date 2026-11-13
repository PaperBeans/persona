package persona.android.navigation

import androidx.navigation3.runtime.NavKey
import org.junit.Assert.assertEquals
import org.junit.Test
import persona.core.navigation.*

/** Explicit home selection must not restore an earlier session; targeted entries must still do so. */
class AgentsTabNavigationTest {
    private val stacks = AppTab.entries.associateWith { mutableListOf<NavKey>(tabRoot(it)) }
    private var selected = AppTab.Persona
    private val navigator = AppNavigator(stacks, { selected }, { selected = it }, chatTab = { AppTab.Agents })
    private val agent = ChatRoute("machine", "agent", ConversationKind.Agent)

    @Test fun selectingAgentsAfterATaskEntryShowsTheList() {
        navigator.navigate(agent)
        assertEquals(listOf(AgentsRoute, agent), stacks.getValue(AppTab.Agents))
        navigator.selectTab(AppTab.Persona)
        navigator.selectTab(AppTab.Agents)
        assertEquals(AppTab.Agents, selected)
        assertEquals(listOf(AgentsRoute), stacks.getValue(AppTab.Agents))
    }

    @Test fun selectingAgentsAfterASessionLinkShowsTheList() {
        val route = Router().parse(RouteInput.DeepLink("persona://open?machine=machine&session=agent&kind=agent"))!!
        selected = route.restore(stacks)
        assertEquals(agent, stacks.getValue(selected).last())
        navigator.selectTab(AppTab.Persona)
        navigator.selectTab(AppTab.Agents)
        assertEquals(listOf(AgentsRoute), stacks.getValue(AppTab.Agents))
    }

    @Test fun selectingAgentsAfterAHubSessionLinkShowsTheList() {
        val hub = "https://hub.example.com"
        org.junit.Assert.assertTrue(persona.android.ui.openHubPage("$hub/s/machine/agent", listOf(hub), null,
            listOf(persona.core.contracts.SessionKey("machine", "agent")), persona.core.contracts.api.AssistantSnapshot(), navigator))
        assertEquals("agent", (stacks.getValue(selected).last() as ChatRoute).sessionId)
        navigator.back()
        assertEquals(AppTab.Persona, selected)
        persona.android.ui.openHubPage("$hub/s/machine/agent", listOf(hub), null, emptyList(),
            persona.core.contracts.api.AssistantSnapshot(), navigator)
        navigator.selectTab(AppTab.Persona)
        navigator.selectTab(AppTab.Agents)
        assertEquals(listOf(AgentsRoute), stacks.getValue(AppTab.Agents))
    }

    @Test fun selectingAgentsAfterANotificationShowsTheList() {
        val route = Router().parse(RouteInput.Notification("machine/agent", ConversationKind.Agent, "request"))!!
        selected = route.restore(stacks)
        assertEquals(InteractionRoute(agent, "request"), stacks.getValue(selected).last())
        navigator.selectTab(AppTab.Persona)
        navigator.selectTab(AppTab.Agents)
        assertEquals(listOf(AgentsRoute), stacks.getValue(AppTab.Agents))
    }

    @Test fun selectingAgentsClearsRestoredDetailsButPreservesTheAssistant() {
        val assistant = ChatRoute("machine", "assistant", ConversationKind.Assistant)
        stacks.getValue(AppTab.Persona).add(assistant)
        stacks.getValue(AppTab.Agents).addAll(listOf(agent, TerminalRoute("machine", "agent")))
        val restored = AppNavigator(stacks, { selected }, { selected = it })
        restored.selectTab(AppTab.Agents)
        assertEquals(listOf(AgentsRoute), stacks.getValue(AppTab.Agents))
        assertEquals(listOf(AssistantRoute, assistant), stacks.getValue(AppTab.Persona))
        restored.selectTab(AppTab.Persona)
        assertEquals(assistant, stacks.getValue(selected).last())
    }

    @Test fun anExplicitNavigatorSelectionOfTheSameTabShowsTheList() {
        navigator.navigate(agent)
        navigator.selectTab(AppTab.Agents)
        assertEquals(listOf(AgentsRoute), stacks.getValue(AppTab.Agents))
        navigator.back()
        assertEquals(listOf(AgentsRoute), stacks.getValue(AppTab.Agents))
    }

    @Test fun targetedEntriesStillOpenDetailsAfterSelectingTheList() {
        navigator.selectTab(AppTab.Agents)
        navigator.selectTab(AppTab.Persona)
        navigator.navigate(agent)
        assertEquals(listOf(AgentsRoute, agent), stacks.getValue(AppTab.Agents))
        navigator.back()
        assertEquals(AppTab.Persona, selected)

        val link = Router().parse(RouteInput.DeepLink("persona://open?machine=machine&session=agent&kind=agent"))!!
        selected = link.restore(stacks)
        assertEquals(agent, stacks.getValue(selected).last())
        navigator.selectTab(AppTab.Persona)
        val notification = Router().parse(RouteInput.Notification("machine/agent", ConversationKind.Agent, "request"))!!
        selected = notification.restore(stacks)
        assertEquals(InteractionRoute(agent, "request"), stacks.getValue(selected).last())
    }
}
