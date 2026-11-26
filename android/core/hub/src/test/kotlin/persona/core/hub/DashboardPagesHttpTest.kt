package persona.core.hub

import kotlinx.coroutines.runBlocking
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.mockwebserver.MockResponse
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import persona.core.contracts.api.AssistantConfigUpdate
import persona.core.contracts.api.ExtensionUiContext
import persona.core.hub.ProtocolCompatibilityMockServer as MockWebServer

/**
 * Dashboards (#15017) through the existing clients: the reserved extension id `-pages` passes the extension id check and
 * keeps its leading dash in the path, its places decode like any extension's, and Settings › Assistant's switch is the
 * `dashboards` field of `/api/v1/assistant`, written only when it changes.
 */
class DashboardPagesHttpTest {
    private val device = ExtensionUiContext(tz = "UTC", locale = "en", platform = "android", uiVersion = 10)

    private fun <T> withHub(block: suspend (MockWebServer, HttpExtensionManagement) -> T): T = runBlocking {
        MockWebServer().use { server ->
            val http = OkHttpClient.Builder().retryOnConnectionFailure(false).build()
            block(server, HttpExtensionManagement({ path, method, body ->
                http.newCall(Request.Builder().url(server.url(path)).method(method, body).build()).execute()
            }, device = { device }, legacy = false))
        }
    }

    @Test fun theReservedIdIsListedDrawnAndPressedOverTheExtensionRoutes() = withHub { server, api ->
        server.enqueue(MockResponse().setBody("""{"ui":[
            {"extension":"example-gateway","id":"usage","title":"Usage","placement":"nav_page","icon":"chart"},
            {"extension":"-pages","extension_name":"Persona","id":"index","title":"Dashboards","placement":"nav_page","size":"full","icon":"chart"},
            {"extension":"-pages","id":"pg_0123456789abcdef","title":"Running log","placement":"nav_page","size":"full","icon":"calendar"}]}"""))
        val places = api.uiPlaces("")
        assertEquals("/api/v1/extensions/-/ui", server.takeRequest().path)
        assertEquals(listOf("example-gateway" to "usage", "-pages" to "index", "-pages" to "pg_0123456789abcdef"),
            places.map { it.extension to it.id })
        assertEquals("Running log", places.last().title)
        assertEquals("Persona", places[1].extensionName)

        server.enqueue(MockResponse().setBody("""{"view":{"title":"Running log","sections":[]}}"""))
        val view = api.renderUi("-pages", "pg_0123456789abcdef", null)
        val drawn = server.takeRequest()
        assertEquals("POST", drawn.method)
        assertEquals("/api/v1/extensions/-pages/-/ui/pg_0123456789abcdef", drawn.path)
        assertTrue(drawn.body.readUtf8().contains(""""ui_version":10"""))
        assertEquals("Running log", view.title)

        server.enqueue(MockResponse().setBody("""{"toast":"Received"}"""))
        api.uiAction("-pages", "index", "open", null, null, mapOf("page" to "pg_0123456789abcdef"))
        val pressed = server.takeRequest()
        assertEquals("/api/v1/extensions/-pages/-/ui/index", pressed.path)
        assertTrue(pressed.body.readUtf8().contains(""""action":"open""""))
    }

    @Test fun theSwitchIsTheDashboardsFieldOfTheAssistantSettings() = runBlocking {
        MockWebServer().use { server ->
            val api = HttpAssistantManagement(server.url("/").toString(), "test-token", OkHttpClient())
            server.enqueue(MockResponse().setBody("""{"machine_id":"server","dashboards":true}"""))
            assertEquals(true, api.config().dashboards)
            assertEquals("/api/v1/assistant", server.takeRequest().path)
            server.enqueue(MockResponse().setBody("""{"machine_id":"server"}"""))
            assertEquals("absent is off", null, api.config().dashboards)
            server.takeRequest()

            server.enqueue(MockResponse().setBody("""{"machine_id":"server","dashboards":false}"""))
            val saved = api.updateConfig(AssistantConfigUpdate(dashboards = false))
            val put = server.takeRequest()
            assertEquals("PUT", put.method)
            assertEquals("/api/v1/assistant", put.path)
            assertEquals("""{"dashboards":false}""", put.body.readUtf8())
            assertEquals(false, saved.dashboards)
            // Any other change leaves the switch alone.
            server.enqueue(MockResponse().setBody("""{"machine_id":"server","dashboards":true}"""))
            api.updateConfig(AssistantConfigUpdate(timezone = "Europe/Paris"))
            assertEquals("""{"timezone":"Europe/Paris"}""", server.takeRequest().body.readUtf8())
        }
    }
}
