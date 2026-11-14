package persona.core.hub

import persona.core.contracts.api.*
import kotlinx.coroutines.flow.flowOf
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.Json
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.mockwebserver.MockResponse
import persona.core.hub.ProtocolCompatibilityMockServer as MockWebServer
import org.junit.Assert.*
import org.junit.Test

class HttpTerminalPanesTest {
    private fun <T> withHub(block: suspend (MockWebServer, HttpTerminalPanes) -> T): T = runBlocking {
        MockWebServer().use { server ->
            val http = OkHttpClient.Builder().retryOnConnectionFailure(false).build()
            block(server, HttpTerminalPanes(flowOf(true)) { path, method, body, _ ->
                http.newCall(Request.Builder().url(server.url(path)).method(method, body).build()).execute()
            })
        }
    }

    // The mock hub's shape (scripts/mock/mock_hub.py mock_panes): an agent pane with its session, shells without.
    @Test fun panesReadTheMachinesHostsWorkspacesTabsAndPanes() = withHub { server, panes ->
        server.enqueue(MockResponse().setBody("""{"hosts":[{"kind":"herdr","stream":true,"control":true,"new_tab":true,"worktree":true,
            "workspaces":[{"id":"w1","label":"persona","number":1,"cwd":"/home/me/persona","branch":"main"}],
            "tabs":[{"id":"w1:t1","workspace_id":"w1","label":"agent-1","number":1}],
            "panes":[{"pane":"w1:p1","terminal":"pane:w1:p1","tab_id":"w1:t1","workspace_id":"w1","agent":"claude","status":"working","title":"Fix login","cwd":"/home/me/persona","session_id":"s-1"},
                     {"pane":"w1:p2","terminal":"pane:w1:p2","tab_id":"w1:t1","workspace_id":"w1","title":"zsh"}]}]}"""))
        val host = panes.panes("desk/a").hosts.single()
        assertEquals("/api/v1/machines/desk%2Fa/panes", server.takeRequest().let { assertEquals("GET", it.method); it.path })
        assertTrue(host.newTab && host.stream && host.control)
        assertEquals(PaneSpace("w1", "persona", 1, "/home/me/persona", "main"), host.workspaces.single())
        assertEquals(PaneTab("w1:t1", "w1", "agent-1", 1), host.tabs.single())
        assertEquals("s-1", host.panes[0].sessionId)
        assertEquals("claude", host.panes[0].agent)
        assertEquals(PaneItem("w1:p2", "pane:w1:p2", "w1:t1", "w1", title = "zsh"), host.panes[1])
    }

    @Test fun aNewTabPostsTheWorkspaceAndItsHostOnly() = withHub { server, panes ->
        server.enqueue(MockResponse().setBody("""{"pane":"w2:p4","terminal":"pane:w2:p4","tab_id":"w2:t4"}"""))
        assertEquals(NewPaneTab("w2:p4", "pane:w2:p4", "w2:t4"), panes.newTab("desk", "w2", "herdr"))
        server.takeRequest().let {
            assertEquals("POST", it.method)
            assertEquals("/api/v1/machines/desk/panes", it.path)
            assertEquals(Json.parseToJsonElement("""{"workspace_id":"w2","kind":"herdr"}"""), Json.parseToJsonElement(it.body.readUtf8()))
        }
        // No host named: the hub uses the machine's own.
        assertEquals(Json.parseToJsonElement("""{"workspace_id":"w2"}"""), newTabBody("w2", null, null))
    }

    @Test fun aHomeTerminalPostsTheHostAndHomeDirectory() = withHub { server, panes ->
        server.enqueue(MockResponse().setBody("""{"pane":"w1:p1","terminal":"pane:w1:p1","tab_id":"w1:t1"}"""))
        panes.newTab("desk", "", "herdr", cwd = "~")
        server.takeRequest().let {
            assertEquals("POST", it.method)
            assertEquals(Json.parseToJsonElement("""{"kind":"herdr","cwd":"~","new_space":true}"""), Json.parseToJsonElement(it.body.readUtf8()))
        }
        assertTrue(HttpTerminalPanes.offersNewSpace(emptySet()))
        assertTrue(HttpTerminalPanes.offersNewSpace(setOf(HttpTerminalPanes.NEW_SPACE)))
        assertFalse(HttpTerminalPanes.offersNewSpace(setOf(HttpTerminalPanes.TERMINAL_PANES)))
    }

    @Test fun aRefusedNewTabSaysTheHubsWords() = withHub { server, panes ->
        server.enqueue(MockResponse().setResponseCode(403).setBody("""{"error":"opening a terminal needs a full device"}"""))
        val failed = runCatching { panes.newTab("desk", "w1") }.exceptionOrNull() as HubFailure
        assertEquals(403, failed.status)
        assertTrue(failed.message.orEmpty().contains("full device"))
    }
}
