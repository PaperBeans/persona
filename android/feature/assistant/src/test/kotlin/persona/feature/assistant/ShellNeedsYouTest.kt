package persona.feature.assistant

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import persona.core.contracts.Interaction
import persona.core.contracts.Session
import persona.core.contracts.SessionKey
import persona.core.domain.inbox.InboxItem
import persona.core.domain.sort.needsYouRows
import persona.core.domain.sort.needsYouWaiting

/** Needs you over the blank page's composer on Glass (#4339 C17, iOS `ShellNeedsYou`): which lines, how many, where the rest go. */
class ShellNeedsYouTest {
    private fun card(id: String, session: String, at: String, kind: String = "plan", title: String? = null) =
        Interaction(id, machineId = "m", sessionId = session, sessionTitle = title, kind = kind, createdAt = at)

    private fun mail(id: String, updated: String) = NeedsYouMessage(InboxItem(id, title = id, preview = "about $id", tier = "action", updatedAt = updated), lark = false)

    @Test fun aLinePerSessionItsOldestCardFirstThenTheInbox() {
        val sessions = listOf(
            Session(machineId = "m", sessionId = "two", title = "Migration"),
            Session(machineId = "m", sessionId = "term", title = "Release", state = "needs_you", stateSince = "2026-11-12T08:30:00Z"),
        )
        val cards = listOf(
            card("late", "two", "2026-11-12T09:00:00Z", kind = "question"),
            card("early", "two", "2026-11-12T08:00:00Z"),
            card("loose", "", "2026-11-12T08:45:00Z", kind = "permission"),
        )
        val rows = needsYouRows(cards, needsYouWaiting(sessions))
        val lines = needsYouBySession(rows, sessions, listOf(mail("old", "2026-11-11T08:00:00Z"), mail("new", "2026-11-12T08:00:00Z")))
        // Rows go oldest first: the two-card session by its older card, the terminal wait, the card on no session.
        assertEquals(listOf("agent:m/two", "agent:m/term", "card:loose", "inbox:new", "inbox:old"), lines.map { it.key })
        val two = lines[0] as NeedsYouLine.Agent
        assertEquals("one line for two cards, its oldest card's", "early", two.requestId)
        assertEquals("plan", two.kind)
        assertEquals("Migration", two.who)
        val term = lines[1] as NeedsYouLine.Agent
        assertTrue("waits only in its terminal", term.terminal)
        assertEquals("", term.requestId)
        assertEquals("screen", term.kind)
        assertEquals("Release", term.who)
        assertFalse(two.terminal)
        // A card on no session keeps its own id, which opens it on Agents (needsYouRoute).
        assertEquals("loose", (lines[2] as NeedsYouLine.Agent).requestId)
    }

    @Test fun asManyAsFitThreeAtMostOneAtLeast() {
        val line = 56
        fun height(total: Int): (Int) -> Int = { n -> line * (n + if (n < total) 1 else 0) }
        // Unmeasured: no limit, three and the line for the rest.
        assertEquals(3, needsYouShown(5, Int.MAX_VALUE, height(5)))
        assertEquals(3, needsYouShown(5, 4 * line, height(5)))
        assertEquals("three and the rest's line need four lines", 2, needsYouShown(5, 4 * line - 1, height(5)))
        assertEquals(1, needsYouShown(5, 2 * line, height(5)))
        assertEquals("one at least, with no room at all", 1, needsYouShown(5, -100, height(5)))
        // With nothing left over there is no line for the rest.
        assertEquals(3, needsYouShown(3, 3 * line, height(3)))
        assertEquals(2, needsYouShown(2, 2 * line, height(2)))
        assertEquals(1, needsYouShown(1, 0, height(1)))
        assertEquals(0, needsYouShown(0, Int.MAX_VALUE, height(0)))
    }

    @Test fun theRestOpenAgentsOnlyWhenTheyAreAllAgents() {
        fun agent(id: String) = NeedsYouLine.Agent(SessionKey("m", id), id, "plan", null, who = id, what = "", row = "agent:m/$id")
        fun inbox(id: String) = NeedsYouLine.Inbox(id, lark = false, itemKind = "mail", who = id, what = "")
        val lines = listOf(agent("a"), agent("b"), agent("c"), agent("d"), inbox("x"))
        assertNull("all shown", needsYouMore(lines.take(3), 3))
        // Agents among the rest: they need you; with a message among them it is the Inbox, which holds only its own.
        assertEquals(NeedsYouMore(2, 1), needsYouMore(lines, 3))
        assertTrue(needsYouMore(lines, 3)!!.needYou)
        assertFalse(needsYouMore(lines, 3)!!.opensAgents)
        val agentsOnly = needsYouMore(lines.take(4), 1)!!
        assertTrue(agentsOnly.needYou)
        assertTrue(agentsOnly.opensAgents)
        val inboxOnly = needsYouMore(listOf(agent("a"), inbox("x"), inbox("y")), 1)!!
        assertFalse(inboxOnly.needYou)
        assertFalse(inboxOnly.opensAgents)
    }

    @Test fun theFoldIsTheSecondRowOfThingsToAsk() {
        // After the lead (index 0): six things to ask two a row, the fold is the second row's two.
        assertEquals(3..4, welcomeFold(1, 6))
        assertEquals(3..3, welcomeFold(1, 3))
        assertEquals(1..2, welcomeFold(1, 2))
        assertEquals(2..2, welcomeFold(2, 1))
        assertEquals("none: the lead", 0..0, welcomeFold(1, 0))
    }
}
