package persona.feature.settings

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.flowOf
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.JsonObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.ExtensionUiEvent
import persona.core.contracts.Hello
import persona.core.contracts.ServerEvent
import persona.core.contracts.api.*
import persona.core.designsystem.PersonaTheme

/**
 * Dashboards (#15017) on Android: the hub's own ids are drawn over HTTP only, the places list is read again on
 * `assistant_pages`, a page's header is its View's title (never its raw id) and every page but the index carries the
 * app's ownership line.
 */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en-w400dp-h900dp")
class DashboardPagesTest {
    @get:Rule val rule = createComposeRule()

    private val pageId = "pg_0123456789abcdef"

    /** A connection that records every frame the app sends; a native page stream starts with one. */
    private class Connection : HubConnection {
        override val state = MutableStateFlow(ConnectionState.Online)
        override val events = MutableSharedFlow<ServerEvent>()
        override var lastHello: Hello? = Hello(features = listOf("extension_ui_stream_v1"), hubId = "synthetic-hub", clientId = "synthetic-principal")
        val sent = mutableListOf<JsonObject>()
        override fun send(message: JsonObject): Boolean { sent.add(message); return true }
        override fun reconnect() {}
    }

    /** A hub that offers the native stream for every page and answers each page over HTTP with [titleFor]. */
    private class Pages(connection: HubConnection, private val titleFor: (String, String) -> String?) : ExtensionApiStub() {
        val rendered = mutableListOf<Pair<String, String>>()
        override val nativeStream = flowOf<ExtensionStreamBinding?>(ExtensionStreamBinding(connection, "synthetic-owner", "synthetic-group", "assistant"))
        override suspend fun renderUi(extension: String, ui: String, context: ExtensionUiContext?, state: String?): ExtensionView {
            rendered += extension to ui
            return ExtensionView(title = titleFor(extension, ui), sections = listOf(ExtensionViewSection(items = listOf(
                ExtensionViewItem("row", title = "Today", value = "5 km")))))
        }
    }

    @Test fun theHubsOwnIdsAreNeverDrawnThroughTheNativeStream() {
        assertTrue(DashboardPages.httpOnly("-pages"))
        assertTrue("every id the hub reserves, not only this one", DashboardPages.httpOnly("-later"))
        assertFalse(DashboardPages.httpOnly("example-gateway"))
        assertFalse(DashboardPages.httpOnly("pages-"))

        val connection = Connection()
        val api = Pages(connection) { _, _ -> "Running log" }
        var page by mutableStateOf("-pages" to pageId)
        rule.setContent { PersonaTheme(reduceMotion = true) { ExtensionUiPage(api, page.first, page.second, "Running log", {}) } }
        rule.waitForIdle()
        rule.onNodeWithText("Today").assertExists()
        assertEquals(listOf("-pages" to pageId), api.rendered)
        assertTrue("no native page stream was opened for -pages", connection.sent.none { it.toString().contains("-pages") })

        // The control: the same hub draws an ordinary extension's page through its native stream, not over HTTP.
        rule.runOnIdle { page = "example-gateway" to "usage" }
        rule.waitForIdle()
        assertEquals(listOf("-pages" to pageId), api.rendered)
        assertTrue("the ordinary page opened its native stream", connection.sent.any { it.toString().contains("example-gateway") })
    }

    @Test fun theHeaderIsThePagesOwnTitleAndNeverItsId() {
        val untitled = "pg_fedcba9876543210"
        val view = mapOf<String, String?>(pageId to "Running log", untitled to " ", "index" to "Dashboards")
        val api = Pages(Connection()) { _, ui -> view[ui] }
        // A deep link opens the page with no title: the View names it.
        var opened by mutableStateOf(pageId to "")
        rule.setContent { PersonaTheme(reduceMotion = true) { ExtensionUiPage(api, "-pages", opened.first, opened.second, {}) } }
        rule.onNodeWithText("Running log").assertExists()
        rule.onNodeWithText(pageId).assertDoesNotExist()

        // A View without a title, opened from a row that fell back to the id: the place's name, not the id.
        rule.runOnIdle { opened = untitled to untitled }
        // waitForIdle drains Robolectric's main looper, where the page's load runs; waitUntil does not.
        rule.waitForIdle()
        assertEquals(listOf("-pages" to pageId, "-pages" to untitled), api.rendered)
        rule.onNodeWithText("Dashboards").assertExists()
        rule.onNodeWithText(untitled).assertDoesNotExist()

        assertEquals("Running log", DashboardPages.headerTitle("-pages", pageId, "Running log", "Old row title") { "Dashboards" })
        assertEquals("Old row title", DashboardPages.headerTitle("-pages", pageId, null, "Old row title") { "Dashboards" })
        assertEquals("Dashboards", DashboardPages.headerTitle("-pages", pageId, "", pageId) { "Dashboards" })
        // Another extension's page keeps its row's title, as before.
        assertEquals("usage", DashboardPages.headerTitle("example-gateway", "usage", null, "usage") { "Dashboards" })
    }

    @Test fun everyDashboardsPageButTheIndexSaysWhoKeepsIt() {
        val api = Pages(Connection()) { _, ui -> if (ui == "index") "Dashboards" else "Running log" }
        var opened by mutableStateOf("-pages" to pageId)
        rule.setContent { PersonaTheme(reduceMotion = true) { ExtensionUiPage(api, opened.first, opened.second, "", {}) } }
        rule.onNodeWithText("Kept up to date by Persona").assertExists()
        rule.runOnIdle { opened = "-pages" to "index" }
        rule.onNodeWithText("Dashboards").assertExists()
        rule.onNodeWithText("Kept up to date by Persona").assertDoesNotExist()
        assertTrue(DashboardPages.showsOwner("-pages", pageId))
        assertFalse(DashboardPages.showsOwner("-pages", "index"))
        assertFalse(DashboardPages.showsOwner("example-gateway", "usage"))
    }

    private class Places(var places: List<ExtensionUiPlace>) : ExtensionApiStub() {
        var asked = 0
        override suspend fun uiPlaces(placement: String, launcher: String?, machine: String?): List<ExtensionUiPlace> {
            if (placement.isEmpty()) asked++
            return if (placement.isEmpty()) places else emptyList()
        }
    }

    @Test fun assistantPagesReadsThePlacesAgain() = runTest {
        val usage = ExtensionUiPlace("example-gateway", "usage", "Usage", "nav_page", icon = "chart")
        val api = Places(listOf(usage))
        val changed = MutableSharedFlow<Unit>()
        val slots = ExtensionSlots(api, MutableStateFlow(true), MutableSharedFlow<ExtensionUiEvent>(), changed)
        backgroundScope.launch { slots.run(MutableStateFlow(true)) }
        runCurrent()
        assertEquals(1, api.asked)
        assertEquals(listOf("usage"), slots.state.value.navPages.map { it.id })

        // The first page kept: Dashboards and a pinned page join the rows without a reconnect.
        api.places = listOf(usage,
            ExtensionUiPlace("-pages", "index", "Dashboards", "nav_page", extensionName = "Persona", size = "full", icon = "chart"),
            ExtensionUiPlace("-pages", pageId, "Running log", "nav_page", size = "full", icon = "calendar"))
        changed.emit(Unit)
        runCurrent()
        assertEquals(2, api.asked)
        assertEquals(listOf("usage", "index", pageId), slots.state.value.navPages.map { it.id })

        // Switched off: the rows go again.
        api.places = listOf(usage)
        changed.emit(Unit)
        runCurrent()
        assertEquals(3, api.asked)
        assertEquals(listOf("usage"), slots.state.value.navPages.map { it.id })
    }

    @Test fun assistantPagesOnAHubWithoutSlotsReadsNothing() = runTest {
        val api = Places(emptyList())
        val changed = MutableSharedFlow<Unit>()
        val slots = ExtensionSlots(api, MutableStateFlow(false), MutableSharedFlow<ExtensionUiEvent>(), changed)
        backgroundScope.launch { slots.run(MutableStateFlow(true)) }
        runCurrent()
        changed.emit(Unit)
        runCurrent()
        assertEquals(0, api.asked)
        assertEquals(ExtensionSlotsState(), slots.state.value)
    }

    @Test fun theShellsSlotsFollowTheHubsEvent() {
        // The public constructor takes the management's placesChanged, which the app feeds from assistant_pages.
        val changed = MutableSharedFlow<Unit>()
        val api = object : ExtensionApiStub() { override val placesChanged = changed }
        val slots = ExtensionSlots(api)
        assertTrue(slots.api === api)
    }
}
