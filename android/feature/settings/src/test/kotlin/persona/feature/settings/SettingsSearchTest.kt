package persona.feature.settings

import org.junit.Assert.*
import org.junit.Test
import persona.core.navigation.SettingsPage

class SettingsSearchTest {
    @Test fun blankSearchShowsAllEntries() {
        assertTrue(settingsSearchMatches("  ", "Models"))
    }
    @Test fun entryNamesAndLocalizedAliasesMatch() {
        assertTrue(settingsSearchMatches("模型", "模型"))
        assertTrue(settingsSearchMatches("保活", "通知与后台", aliases = "通知 电池 保活 自启动"))
        assertTrue(settingsSearchMatches("自動起動", "通知とバックグラウンド", aliases = "自動起動"))
        assertTrue(settingsSearchMatches("Ｃｏｄｅｘ", "Agent", aliases = "Claude Code Codex session triage"))
        assertTrue(settingsSearchMatches("claude code", "Agent", aliases = "Claude Code Codex"))
        assertFalse(settingsSearchMatches("claude missing", "Agent", aliases = "Claude Code Codex"))
        assertFalse(settingsSearchMatches("<hub address>", "Connection", aliases = "Hub server network"))
    }
    @Test fun everyDestinationStillRoundTrips() {
        SettingsPage.entries.forEach { assertEquals(it, settingsPageFromInitialPage(it.toInitialPage())) }
        assertEquals(SettingsPage.Notifications, settingsPageFromInitialPage("notify"))
        assertEquals(SettingsPage.Readiness, settingsPageFromInitialPage("ready"))
        assertEquals(SettingsPage.Capsule, settingsPageFromInitialPage("capsule"))
        assertEquals(SettingsPage.Diagnostics, settingsPageFromInitialPage("diag"))
        assertEquals(SettingsPage.Sent, settingsPageFromInitialPage("sent"))
    }
    @Test fun newAgentGroupKeepsAssistantScope() {
        assertTrue(isAssistantSettingsPage("agent-settings"))
        assertFalse(isAssistantSettingsPage("notification-background"))
    }
}
