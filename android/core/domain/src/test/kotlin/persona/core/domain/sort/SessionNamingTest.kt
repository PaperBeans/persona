package persona.core.domain.sort

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import persona.core.contracts.CoreText
import persona.core.contracts.Session

class SessionNamingTest {
    private val herdrSession = Session(
        machineId = "mac",
        sessionId = "s1",
        title = "CLI session name",
        cwd = "/work/export",
        agent = "claude",
        spaceKind = "herdr",
        spaceName = "persona",
        slotId = "w1:t1",
        slotName = "Export work",
        paneId = "w1:p1",
    )

    @Test fun herdrTabNameMatchesOnlyNonAssistantHerdrSessionsWithTabAssociation() {
        assertEquals("Export work", herdrSession.herdrTabName)
        assertEquals("Trimmed", herdrSession.copy(slotName = "  Trimmed  ").herdrTabName)
        assertEquals("Export work", herdrSession.copy(spaceKind = null, host = "herdr-core", slotId = null, tabId = "t1").herdrTabName)
        assertEquals("Export work", herdrSession.copy(spaceKind = null, host = null, paneId = null).herdrTabName)
        assertNull(herdrSession.copy(assistant = true).herdrTabName)
        assertNull(herdrSession.copy(slotId = null, tabId = null).herdrTabName)
        assertNull(herdrSession.copy(slotName = "   ").herdrTabName)
        assertNull(herdrSession.copy(slotName = null).herdrTabName)
        assertNull(herdrSession.copy(spaceKind = "tern").herdrTabName)
        assertNull(herdrSession.copy(spaceKind = null, host = "orca").herdrTabName)
        assertNull(herdrSession.copy(spaceKind = null, host = null, paneId = "botmux:1").herdrTabName)
    }

    @Test fun displayTitleAndDetailKeepDefaultOffUnchangedAndPromoteTabWhenEnabled() {
        assertEquals("CLI session name", herdrSession.displayTitle(preferTab = false))
        assertNull(herdrSession.displayTabName(preferTab = false))
        assertNull(herdrSession.displayTitleDetail(preferTab = false))
        assertFalse(herdrSession.usesUnifiedHerdrRename(preferTab = false))

        assertEquals("Export work", herdrSession.displayTitle(preferTab = true))
        assertEquals("Export work", herdrSession.displayTabName(preferTab = true))
        assertEquals("CLI session name", herdrSession.displayTitleDetail(preferTab = true))
        assertTrue(herdrSession.usesUnifiedHerdrRename(preferTab = true))

        assertNull(herdrSession.copy(title = "Export work").displayTitleDetail(preferTab = true))
        assertNull(herdrSession.copy(title = "  ").displayTitleDetail(preferTab = true))
        val untabbed = herdrSession.copy(slotId = null, tabId = null, slotName = null)
        assertEquals("CLI session name", untabbed.displayTitle(preferTab = true))
        assertNull(untabbed.displayTitleDetail(preferTab = true))
        assertFalse(untabbed.usesUnifiedHerdrRename(preferTab = true))
    }

    @Test fun unifiedRenameSyncsCliTitleOnlyForClaudeAndCodex() {
        assertEquals(
            SessionRename(title = "Updated export", tabName = "Updated export"),
            SessionRename.unifiedChanges(herdrSession, " Updated export "),
        )
        assertEquals(
            SessionRename(title = "Codex export", tabName = "Codex export"),
            SessionRename.unifiedChanges(herdrSession.copy(agent = "codex"), "Codex export"),
        )
        assertEquals(
            SessionRename(title = null, tabName = "Tab only"),
            SessionRename.unifiedChanges(herdrSession.copy(agent = "opencode"), "Tab only"),
        )
        // Keeping the tab name while Claude's CLI title still differs allows syncing them.
        assertEquals(
            SessionRename(title = "Export work", tabName = "Export work"),
            SessionRename.unifiedChanges(herdrSession, "Export work"),
        )
        // When both already match, Save stays disabled.
        assertFalse(SessionRename.unifiedChanges(herdrSession.copy(title = "Export work"), "Export work").changed)
        // Non-Claude/Codex with unchanged tab name has nothing to save.
        assertFalse(SessionRename.unifiedChanges(herdrSession.copy(agent = "opencode"), "Export work").changed)
        assertFalse(SessionRename.unifiedChanges(herdrSession, "  ").changed)
        assertFalse(SessionRename.unifiedChanges(herdrSession.copy(slotId = null, tabId = null), "New").changed)
    }

    @Test fun rowContentMovesCliTitleToPlaceLineAndSuppressesDuplicateTabWhenPreferringTab() {
        val row = AgentSession(herdrSession)
        val off = row.rowContent(AgentRowContext(located = true, machine = "MacBook", preferHerdrTabName = false))
        assertEquals(listOf(CoreText.plain("persona › Export work"), CoreText.plain("MacBook")), off.place)

        val on = row.rowContent(AgentRowContext(located = true, machine = "MacBook", preferHerdrTabName = true))
        assertEquals(listOf(CoreText.plain("CLI session name"), CoreText.plain("persona"), CoreText.plain("MacBook")), on.place)

        val onInsideSpace = row.rowContent(AgentRowContext(located = false, preferHerdrTabName = true))
        assertEquals(listOf(CoreText.plain("CLI session name")), onInsideSpace.place)

        val matchingTitles = AgentSession(herdrSession.copy(title = "Export work"))
            .rowContent(AgentRowContext(located = false, preferHerdrTabName = true))
        assertEquals(emptyList<CoreText>(), matchingTitles.place)
    }
}
