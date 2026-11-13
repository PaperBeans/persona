package persona.core.domain

import kotlinx.serialization.json.Json
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import persona.core.contracts.Item

/** Tools no table names read as words, never their raw names (#2986, #4339 C1); the cases are iOS's `ToolNamingTests`. */
class ToolNamingTest {
    @Test fun identifiersReadAsWords() {
        listOf(
            "create_issue" to "Create issue", "createIssue" to "Create issue", "list-pull-requests" to "List pull requests",
            "getPRComments" to "Get PR comments", "HTTPServerStart" to "HTTP server start",
            "browser_take_screenshot" to "Browser take screenshot", "search_github_repos" to "Search GitHub repos",
            "get_url" to "Get URL", "ios_build" to "iOS build", "ShareOnboardingGuide" to "Share onboarding guide",
            "x" to "X", "" to "",
        ).forEach { (identifier, expected) -> assertEquals(identifier, expected, ToolNaming.humanize(identifier)) }
    }

    @Test fun serversReadAsNames() {
        listOf(
            "github" to "GitHub", "chrome-devtools" to "Chrome DevTools", "google_drive" to "Google Drive",
            "plugin_context7_context7" to "Context7", "plugin_lark_docs" to "Lark Docs", "claude_ai_Gmail" to "Gmail",
            "ide" to "IDE", "linear" to "Linear",
        ).forEach { (server, expected) -> assertEquals(server, expected, ToolNaming.serverName(server)) }
    }

    @Test fun otherServersToolsReadAsServerAndTool() {
        assertEquals("GitHub · Create issue", ToolNaming.mcpLabel("mcp__github__create_issue"))
        assertEquals("IDE · Get diagnostics", ToolNaming.mcpLabel("mcp__ide__getDiagnostics"))
        assertEquals("Playwright · Browser navigate", ToolNaming.mcpLabel("mcp__plugin_playwright_playwright__browser_navigate"))
        assertEquals("Gmail · Search threads", ToolNaming.mcpLabel("mcp__claude_ai_Gmail__search_threads"))
        // Persona's own tools keep their names; built-in tools are not MCP tools.
        assertNull(ToolNaming.mcpLabel("mcp__persona__agent_read"))
        assertNull(ToolNaming.mcpLabel("Read"))
        assertNotNull(namedToolVerb("mcp__persona__agent_read"))
    }

    @Test fun aToolNoTableKnowsReadsInWords() {
        assertEquals("GitHub · Create issue", toolVerb("mcp__github__create_issue").zh())
        // A table's bare name under another server is that server's tool, not the built-in one.
        assertNull(namedToolVerb("mcp__files__Read"))
        assertEquals("Files · Read", toolVerb("mcp__files__Read").zh())
        assertEquals("Share onboarding guide", toolVerb("ShareOnboardingGuide").zh())
        // The tables still name what they know.
        assertEquals(toolVerb("Read").zh(), namedToolVerb("Read")!!.zh())
    }

    private fun call(name: String, input: String) = Item("call", kind = "tool_use", toolName = name, toolInput = Json.parseToJsonElement(input))

    // More cases, the shared ones, are in spec/fixtures/tool-names (ToolNamingFixtureTest).
    @Test fun anUnknownToolsRowSaysWhatItIsAbout() {
        assertEquals("Crash on launch", toolSubject(call("mcp__github__create_issue", """{"repo":"r","title":"Crash on launch"}""")))
        assertEquals("Store.swift", toolSubject(call("SomethingNew", """{"path":"/a/b/Store.swift","count":3}""")))
        // A tool a table names keeps its words as its row's title (iOS picks for these too; Android's rows show no verb).
        assertEquals("", toolSubject(call("mcp__persona__kb_search", """{"query":"release notes"}""")))
        // A permission card keeps its own fallback for them (the hub's summary, the description, the input).
        assertEquals("", toolSubject(call("mcp__github__create_issue", """{"title":"Crash on launch"}"""), pick = false))
    }

    @Test fun aShellToolsDetailIsItsCommandLine() {
        assertEquals("$ ls", toolDetail(call("Bash", """{"command":"ls","description":"List"}""")))
        assertEquals("PS> dir", toolDetail(call("PowerShell", """{"command":"dir"}""")))
        assertEquals("wss://e.example.com", toolDetail(call("Monitor", """{"ws":{"url":"wss://e.example.com"},"description":"Events"}""")))
        // Another server's tool of the same name lists its input.
        assertEquals("command: ls", toolDetail(call("mcp__shell__Bash", """{"command":"ls"}""")))
    }

    @Test fun aSentenceReadsInTheTextFaceATokenInTheCodeFace() {
        val args = ToolNaming.arguments(Json.parseToJsonElement("""{"title":"导出很慢","path":"/a/b.kt","id":"a1b2"}"""))
        assertEquals(listOf("title", "path", "id"), args.map { it.id })
        assertFalse(args[0].code)
        assertTrue(args[1].code && args[2].code)
        assertEquals("ID", args[2].label)
    }
}
