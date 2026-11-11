package persona.feature.agents

import kotlinx.serialization.json.Json
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import persona.core.contracts.api.AgentLauncher
import persona.core.contracts.api.LauncherHealth

/** Blocked launchers in New Agent (#4530), as iOS `LauncherHealthTests`: what is grayed, why, and where the form starts. */
class LauncherAvailabilityTest {
    private fun launcher(name: String, state: String? = null, kind: String? = null, error: String? = null) =
        AgentLauncher(name, health = state?.let { LauncherHealth(it, kind, error) })

    @Test fun healthIsReadFromTheLaunchersList() {
        val json = Json { ignoreUnknownKeys = true }
        val l = json.decodeFromString<AgentLauncher>("""{"name":"work_gw","model":"glm","health":{"state":"blocked","kind":"not_in_terminal",
            "checked_at":"2026-11-11T10:00:00Z","error":"bash: work_gw: command not found","resets_at":"2026-11-11T16:00:00Z","x":1}}""")
        assertEquals(LauncherHealth("blocked", "not_in_terminal", "bash: work_gw: command not found", "2026-11-11T10:00:00Z", "2026-11-11T16:00:00Z"), l.health)
        assertNull(json.decodeFromString<AgentLauncher>("""{"name":"claude"}""").health)
    }

    @Test fun onlyBlockedIsGrayedWithOneLinePerClass() {
        for (state in listOf("ok", "working", "failing", "repairing", "")) assertNull(state, launcher("a", state, "auth").block)
        assertNull(launcher("a").block)
        val reasons = mapOf("not_in_terminal" to R.string.agents_launcher_blocked_not_in_terminal, "auth" to R.string.agents_launcher_blocked_auth,
            "model_route" to R.string.agents_launcher_blocked_model_route, "cli_outdated" to R.string.agents_launcher_blocked_cli_outdated,
            "timeout" to R.string.agents_launcher_blocked_timeout, "weird" to R.string.agents_launcher_blocked_other, "" to R.string.agents_launcher_blocked_other)
        for ((kind, reason) in reasons) assertEquals(kind, reason, launcher("a", "blocked", kind).block!!.reason)
        assertEquals(R.string.agents_launcher_blocked_other, launcher("a", "blocked").block!!.reason)
        val terminal = launcher("a", "blocked", "not_in_terminal", "  oops \n").block!!
        assertTrue(terminal.notInTerminal)
        assertEquals("oops", terminal.detail)
        assertEquals(R.string.agents_launcher_help_title_terminal, terminal.title)
        assertNull(launcher("a", "blocked", "auth", "  ").block!!.detail)
        assertEquals(R.string.agents_launcher_help_title_computer, launcher("a", "blocked", "auth").block!!.title)
    }

    @Test fun theFormStartsOnOneThatIsNotBlocked() {
        val blocked = launcher("work_gw", "blocked", "not_in_terminal")
        val list = listOf(launcher("claude", "failing"), blocked, launcher("sonnet"))
        assertEquals("sonnet", initialLauncher("sonnet", list))
        assertEquals("claude", initialLauncher("work_gw", list))
        assertEquals("claude", initialLauncher("gone", list))
        assertEquals("claude", initialLauncher(null, list))
        val allBlocked = listOf(blocked, launcher("team_gw", "blocked", "auth"))
        assertEquals("team_gw", initialLauncher("team_gw", allBlocked))
        assertEquals("work_gw", initialLauncher(null, allBlocked))
        assertEquals("", initialLauncher("x", emptyList()))
        // New Agent's own start, with nothing to follow
        assertEquals("claude", defaultLauncher(NewAgentModelConfig.Unsupported, AgentDefaults(launcher = "work_gw"), list))
    }
}
