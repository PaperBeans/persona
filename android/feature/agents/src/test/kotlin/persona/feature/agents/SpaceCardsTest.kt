package persona.feature.agents

import org.junit.Assert.assertEquals
import org.junit.Test
import persona.core.contracts.Session
import persona.core.domain.sort.AgentSession
import persona.core.domain.sort.agentTabs

/** The space page's cards (#4338 A11): iOS `SpaceView`'s order, `Fmt.path` and `TabPanes.paneName`. */
class SpaceCardsTest {
    private fun agent(id: String, state: String, slot: Int?, place: Int? = null, agent: String = "claude", tabbed: Boolean = true) =
        AgentSession(Session(machineId = "server", sessionId = id, state = state, agent = agent, spaceKind = "herdr", spaceId = "w1",
            spaceName = "app", slotId = slot?.takeIf { tabbed }?.let { "t$it" }, slotIndex = slot, paneId = "w1:p$id", paneIndex = place))

    @Test fun cardsGoByUrgencyWithEachTabsAgentsTogetherInPaneOrder() {
        val launcher = agent("w", "working", 1, 0)
        val asks = agent("2e", "needs_you", 1, 1, agent = "codex")
        val done = agent("31", "unread", 1, 2)
        val deps = agent("3a", "working", 2, 0)
        val cacheA = agent("40", "unread", 3, 1)
        val cacheB = agent("41", "idle", 3, 2)
        val watch = agent("50", "working", 4, 0)
        // The needs-you agent pulls its tab to the top in pane order; the next unread pulls the cache tab; the seen one
        // was already placed with its tab; the working ones follow by tab.
        assertEquals(listOf(launcher, asks, done, cacheA, cacheB, deps, watch).map { it.id },
            spaceCardOrder(listOf(watch, cacheB, deps, done, launcher, cacheA, asks)).map { it.id })
    }

    @Test fun agentsOutsideTabsGoByStateThenTabThenId() {
        val idle = agent("a", "idle", 1, tabbed = false)
        val working2 = agent("c", "working", 2, tabbed = false)
        val working1b = agent("e", "working", 1, tabbed = false)
        val working1a = agent("d", "working", 1, tabbed = false)
        val unread = agent("b", "unread", 9, tabbed = false)
        val asks = agent("f", "needs_you", 5, tabbed = false)
        assertEquals(listOf(asks, unread, working1a, working1b, working2, idle).map { it.id },
            spaceCardOrder(listOf(idle, working2, working1b, working1a, unread, asks)).map { it.id })
    }

    @Test fun pathsShortenTheHomeFolderToATilde() {
        assertEquals("~/code/app", shortPath("/home/me/code/app", "/home/me"))
        assertEquals("~/code/app", shortPath("/home/me/code/app", "/home/me/"))
        assertEquals("~", shortPath("/srv/me", "/srv/me"))
        assertEquals("~/x", shortPath("/srv/me/x", "/srv/me"))
        // A home only matches a whole folder.
        assertEquals("/srv/alex/x", shortPath("/srv/alex/x", "/srv/al"))
        // Without the computer's home, the usual ones.
        assertEquals("~/code/app", shortPath("/Users/me/code/app", null))
        assertEquals("~/code/app", shortPath("/home/me/code/app", ""))
        assertEquals("~/p", shortPath("/data00/home/me/p", null))
        assertEquals("~", shortPath("/home/me", null))
        assertEquals("/opt/app", shortPath("/opt/app", "/"))
        assertEquals("/opt/app", shortPath("/opt/app", null))
    }

    @Test fun tabMatesAreNamedByAgentAndNumberedWhenTheyShareOne() {
        val starter = agent("p0", "working", 1, 0)
        val codex = agent("p1", "working", 1, 1, agent = "codex")
        val second = agent("p2", "working", 1, 2)
        val third = agent("p3", "working", 1, 3)
        val tab = agentTabs(listOf(third, starter, second, codex)).values.single()
        assertEquals(listOf("Claude", "Codex", "Claude 1", "Claude 2"), listOf(starter, codex, second, third).map(tab::paneName))
        // No starter (no pane at index 0): peers, numbered alike.
        val peers = agentTabs(listOf(agent("q1", "unread", 3, 1), agent("q2", "idle", 3, 2))).values.single()
        assertEquals(listOf("Claude 1", "Claude 2"), peers.members.map(peers::paneName))
    }
}
