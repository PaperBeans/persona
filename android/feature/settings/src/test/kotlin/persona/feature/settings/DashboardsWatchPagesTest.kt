package persona.feature.settings

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.test.assertIsOff
import androidx.compose.ui.test.assertIsOn
import androidx.compose.ui.test.hasScrollToNodeAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onFirst
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollToNode
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.flowOf
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.boolean
import kotlinx.serialization.json.decodeFromJsonElement
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.ExtensionUiEvent
import persona.core.contracts.JsonCodec
import persona.core.contracts.api.*
import persona.core.designsystem.*
import persona.core.designsystem.icons.PhIcons
import persona.core.hub.HubError
import persona.core.hub.HubFailure
import java.io.File

/**
 * Dashboards phase 2 watch pages (#13020) on Android:
 * - all 15 shared fixture `draws` cases in `spec/fixtures/dashboards/watch.json` decode and render (watch section and
 *   Dashboards index glance row),
 * - footer actions (`persona.refresh`, `persona.pause`, `persona.resume`, `persona.alert` switch with immediate local
 *   flip and rollback on failure),
 * - defensive filter: Settings › Extensions omits `overview_card` places whose extension id starts with `-`,
 * - sidebar nav icons support watch page icons.
 */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en-w400dp-h1200dp")
class DashboardsWatchPagesTest {
    @get:Rule val rule = createComposeRule()

    private fun watchFixture(): JsonObject {
        val file = File(requireNotNull(System.getProperty("persona.repo.root")), "spec/fixtures/dashboards/watch.json")
        return JsonCodec.parseToJsonElement(file.readText()).jsonObject
    }

    private fun glanceRowItem(name: String, title: String, defaultIcon: String, glance: JsonObject): ExtensionViewItem {
        val value = glance["value"]?.jsonPrimitive?.content
        val icon = glance["icon"]?.jsonPrimitive?.content ?: defaultIcon
        val iconColor = glance["icon_color"]?.jsonPrimitive?.content
        val spark = glance["spark"]?.let { JsonCodec.decodeFromJsonElement(ExtensionSpark.serializer(), it) }
        return ExtensionViewItem(
            type = "row",
            key = "page:$name",
            title = title,
            subtitle = if (value == "—") "Not checked yet" else "Checked today 08:02",
            icon = icon,
            iconColor = iconColor,
            value = value,
            spark = spark,
            action = ExtensionViewAction("open", push = ExtensionViewPush(ui = "pg_0123456789abcdef")),
        )
    }

    private class MapPages(private val views: Map<String, ExtensionView>) : ExtensionApiStub() {
        override suspend fun renderUi(extension: String, ui: String, context: ExtensionUiContext?, state: String?): ExtensionView =
            views.getValue(ui)
    }

    @Test fun all15WatchFixtureDrawsDecodeAndRender() {
        val draws = watchFixture().getValue("draws").jsonArray
        assertEquals("spec/fixtures/dashboards/watch.json must have 15 draw cases", 15, draws.size)

        val defaultIcons = mapOf("number" to "money", "status" to "wifi", "table" to "list", "trend" to "trend_up")
        val pageViews = linkedMapOf<String, ExtensionView>()
        val indexRows = mutableListOf<ExtensionViewItem>()

        for (entry in draws) {
            val obj = entry.jsonObject
            val name = obj.getValue("name").jsonPrimitive.content
            val watch = obj.getValue("watch").jsonObject
            val display = watch.getValue("display").jsonPrimitive.content
            val question = watch.getValue("question").jsonPrimitive.content
            val tripped = obj.getValue("tripped").jsonPrimitive.boolean
            val section = JsonCodec.decodeFromJsonElement(ExtensionViewSection.serializer(), obj.getValue("section"))
            val glance = obj.getValue("glance_row").jsonObject

            assertEquals("$name: watch section key", "persona.watch", section.key)
            assertTrue("$name: every section item must be drawable", section.items.isNotEmpty() && section.items.all(ExtensionViewText::drawable))

            val metric = section.items.first()
            assertEquals("$name: first item is metric", "metric", metric.type)
            assertEquals("$name: metric key", "persona.reading", metric.key)
            assertEquals("$name: metric label matches watch question", question, metric.label)
            assertFalse("$name: metric value non-blank", metric.value.isNullOrBlank())
            assertFalse("$name: metric detail non-blank", metric.detail.isNullOrBlank())

            when (display) {
                "status" -> {
                    assertEquals("$name: status light uses icon_color, not tone", Tone.Neutral, ExtensionViewText.tone(metric.tone))
                    assertNotNull("$name: status light has icon", metric.icon)
                    assertNotNull("$name: status light icon resolves", ExtensionIcons.of(metric.icon))
                    assertNotNull("$name: status light icon_color resolves", ExtensionViewText.hue(metric.iconColor))
                }
                else -> {
                    if (tripped) {
                        assertEquals("$name: tripped tone is caution", Tone.Caution, ExtensionViewText.tone(metric.tone))
                        assertEquals("$name: tripped icon is warning", "warning", metric.icon)
                        assertSame("$name: warning icon resolves", PhIcons.Warning, ExtensionIcons.of(metric.icon))
                    } else {
                        assertEquals("$name: untripped tone is neutral", Tone.Neutral, ExtensionViewText.tone(metric.tone))
                    }
                }
            }

            if (name == "table_normal" || name == "table_alert") {
                assertEquals("$name: table display with rows has metric + table", 2, section.items.size)
                val table = section.items[1]
                assertEquals("$name: second item is table", "table", table.type)
                assertEquals("$name: table key", "persona.rows", table.key)
                val expectedCols = watch.getValue("columns").jsonArray.map { it.jsonPrimitive.content }
                assertEquals("$name: table columns", expectedCols, table.columns.orEmpty().map { it.title })
                val readings = obj.getValue("readings").jsonArray
                val expectedRows = readings.last().jsonObject.getValue("rows").jsonArray.map { r ->
                    (r as JsonArray).map { it.jsonPrimitive.content }
                }
                assertEquals("$name: table rows", expectedRows, ExtensionViewText.tableRows(table))
            } else {
                assertEquals("$name: single metric item", 1, section.items.size)
            }

            if (name == "trend_normal" || name == "trend_alert") {
                val spark = ExtensionViewText.spark(metric.spark)
                assertNotNull("$name: metric spark decodes", spark)
                assertTrue("$name: metric spark fill", spark!!.fill)
                if (tripped) assertEquals("$name: tripped spark tone", Tone.Caution, spark.tone)
                else assertNull("$name: normal spark has no tone", spark.tone)
            } else {
                assertNull("$name: no spark expected", metric.spark)
            }

            val row = glanceRowItem(name, "Watch $name", defaultIcons.getValue(display), glance)
            assertTrue("$name: glance row drawable", ExtensionViewText.drawable(row))
            assertEquals("$name: glance row value", glance.getValue("value").jsonPrimitive.content, row.value)
            glance["icon"]?.jsonPrimitive?.content?.let { iconName ->
                assertEquals("$name: glance icon", iconName, row.icon)
                assertTrue("$name: glance icon is known", ExtensionIcons.of(iconName) != PhIcons.PuzzlePiece)
            }
            glance["icon_color"]?.jsonPrimitive?.content?.let { color ->
                assertNotNull("$name: glance icon_color resolves", ExtensionViewText.hue(color))
            }
            if (name == "trend_normal" || name == "trend_alert") {
                val rowSpark = ExtensionViewText.spark(row.spark)
                assertNotNull("$name: glance spark decodes", rowSpark)
                assertTrue("$name: glance spark fill", rowSpark!!.fill)
                if (tripped) assertEquals("$name: tripped glance spark tone", Tone.Caution, rowSpark.tone)
            }

            val pageId = "pg_%016x".format(pageViews.size + 1)
            pageViews[pageId] = ExtensionView(title = "Watch $name", sections = listOf(section))
            indexRows += row
        }

        // Render all 15 watch pages and the combined Dashboards index through ExtensionUiPage.
        pageViews[DashboardPages.INDEX] = ExtensionView(
            title = "Dashboards",
            sections = listOf(ExtensionViewSection(key = "all", title = "All", items = indexRows)),
        )
        val api = MapPages(pageViews)
        var currentUi by mutableStateOf(pageViews.keys.first())
        rule.setContent {
            PersonaTheme(reduceMotion = true) {
                ExtensionUiPage(api, DashboardPages.EXTENSION, currentUi, "", onBack = {})
            }
        }

        val pageIds = pageViews.keys.filter { it != DashboardPages.INDEX }
        for ((index, entry) in draws.withIndex()) {
            val obj = entry.jsonObject
            val name = obj.getValue("name").jsonPrimitive.content
            val section = JsonCodec.decodeFromJsonElement(ExtensionViewSection.serializer(), obj.getValue("section"))
            val metric = section.items.first()
            rule.runOnIdle { currentUi = pageIds[index] }
            rule.waitForIdle()
            rule.onNodeWithText("Watch $name").assertExists()
            rule.onNodeWithText("Kept up to date by Persona").assertExists()
            rule.onNodeWithText(metric.label!!).assertExists()
            rule.onNodeWithText(metric.value!!).assertExists()
            rule.onNodeWithText(metric.detail!!).assertExists()
            if (section.items.size > 1) {
                val table = section.items[1]
                for (col in table.columns.orEmpty()) rule.onNodeWithText(col.title).assertExists()
                for (row in table.rows.orEmpty()) for (cell in row) rule.onNodeWithText(cell).assertExists()
            }
            if (name == "trend_normal") rule.onNodeWithContentDescription("78.2 → 77.9").assertExists()
            if (name == "trend_alert") rule.onNodeWithContentDescription("78.2 → 80.3").assertExists()
        }

        // Render the Dashboards index with all 15 glance rows (no ownership line on index).
        rule.runOnIdle { currentUi = DashboardPages.INDEX }
        rule.waitForIdle()
        rule.onNodeWithText("Dashboards").assertExists()
        rule.onNodeWithText("Kept up to date by Persona").assertDoesNotExist()
        // Scroll the lazy index list to verify every glance row renders with its value and trend sparklines.
        for (row in indexRows) {
            rule.onNode(hasScrollToNodeAction()).performScrollToNode(hasText(row.title!!))
            rule.onNodeWithText(row.title!!).assertExists()
            if (row.key == "page:trend_normal") rule.onNodeWithContentDescription("78.2 → 77.9").assertExists()
            if (row.key == "page:trend_alert") rule.onNodeWithContentDescription("78.2 → 80.3").assertExists()
        }
    }

    private class WatchFooterHub(private val pageId: String) : ExtensionApiStub() {
        var tripped = false
        var refreshing = false
        var paused = false
        var alertOn = false
        val sentActions = mutableListOf<Pair<String, Map<String, String>?>>()
        var nextAlertGate: CompletableDeferred<Unit>? = null
        var failNextAlert: Exception? = null
        private val eventsFlow = MutableSharedFlow<ExtensionUiEvent>(extraBufferCapacity = 8)
        override val uiEvents = eventsFlow

        fun emitInvalidate() {
            eventsFlow.tryEmit(ExtensionUiEvent(DashboardPages.EXTENSION, pageId, invalidate = true))
        }

        fun view(): ExtensionView {
            val watchItem = if (tripped) {
                ExtensionViewItem(
                    type = "metric", key = "persona.reading",
                    label = "Rent for a 1-bed on Elm Street", value = "1,985.5 USD",
                    detail = "Below 2,000 USD · Checked today 08:02", tone = "caution", icon = "warning",
                )
            } else {
                ExtensionViewItem(
                    type = "metric", key = "persona.reading",
                    label = "Rent for a 1-bed on Elm Street", value = "2,150 USD",
                    detail = "Checked today 08:02",
                )
            }
            val footerItems = buildList {
                add(ExtensionViewItem(
                    type = "text", key = "persona.updated", style = "caption",
                    text = if (paused) "Checked today 08:02" else "Checked today 08:02 · next check today 20:00",
                ))
                if (paused) add(ExtensionViewItem(type = "text", key = "persona.paused", style = "caption", text = "Updates paused"))
                if (refreshing) add(ExtensionViewItem(type = "text", key = "persona.status", style = "caption", text = "Checking now…"))
                if (!paused) add(ExtensionViewItem(type = "button", key = "persona.refresh", title = "Refresh now", action = ExtensionViewAction("persona.refresh")))
                add(ExtensionViewItem(
                    type = "button", key = "persona.pause",
                    title = if (paused) "Resume updates" else "Pause updates",
                    action = ExtensionViewAction(if (paused) "persona.resume" else "persona.pause"),
                ))
                add(ExtensionViewItem(
                    type = "row", key = "persona.alert", title = "Alert me", subtitle = "Below 2,000 USD",
                    control = ExtensionRowControl(kind = "switch", name = "on", on = alertOn, action = ExtensionViewAction("persona.alert")),
                ))
                add(ExtensionViewItem(type = "button", key = "persona.pin", title = "Unpin from sidebar", action = ExtensionViewAction("persona.unpin")))
                add(ExtensionViewItem(type = "button", key = "persona.rename", title = "Rename",
                    action = ExtensionViewAction("persona.rename", push = ExtensionViewPush(state = "rename", title = "Rename", ui = pageId))))
                add(ExtensionViewItem(type = "button", key = "persona.remove", title = "Remove", role = "destructive",
                    action = ExtensionViewAction("persona.remove", confirm = "Remove this page? You can restore it from Dashboards for 30 days.")))
            }
            return ExtensionView(
                title = "Elm Street rent",
                sections = listOf(
                    ExtensionViewSection(key = "persona.watch", items = listOf(watchItem)),
                    ExtensionViewSection(key = "persona.footer", items = footerItems),
                ),
            )
        }

        override suspend fun renderUi(extension: String, ui: String, context: ExtensionUiContext?, state: String?): ExtensionView = view()

        override suspend fun uiAction(
            extension: String, ui: String, action: String, context: ExtensionUiContext?, state: String?,
            values: Map<String, String>?,
        ): ExtensionUiResult {
            sentActions += action to values
            return when (action) {
                "persona.refresh" -> {
                    refreshing = true
                    ExtensionUiResult(view = view(), toast = "Checking now…")
                }
                "persona.pause" -> {
                    paused = true
                    refreshing = false
                    ExtensionUiResult(view = view(), toast = "Updates paused")
                }
                "persona.resume" -> {
                    paused = false
                    ExtensionUiResult(view = view(), toast = "Updates resumed")
                }
                "persona.alert" -> {
                    nextAlertGate?.await()
                    failNextAlert?.let { err ->
                        failNextAlert = null
                        throw err
                    }
                    alertOn = values?.get("on") == "true"
                    ExtensionUiResult(view = view(), toast = if (alertOn) "Alerts on" else "Alerts off")
                }
                else -> ExtensionUiResult(view = view())
            }
        }
    }

    @Test fun watchFooterActionsRefreshPauseResumeAndAlertSwitchRollback() {
        val pageId = "pg_1a2b3c4d5e6f7081"
        val hub = WatchFooterHub(pageId)
        val notices = mutableListOf<String>()
        val feedback = object : AppFeedback {
            override fun failure(error: Throwable, operation: Boolean): Boolean = false
            override fun notice(message: String): Boolean { notices += message; return true }
        }
        rule.setContent {
            CompositionLocalProvider(LocalAppFeedback provides feedback) {
                PersonaTheme(reduceMotion = true) {
                    ExtensionUiPage(hub, DashboardPages.EXTENSION, pageId, "", onBack = {})
                }
            }
        }
        rule.waitForIdle()

        // Initial footer state: Checked/next line, Refresh now, Pause updates, Alert me switch OFF, no Show on Overview.
        rule.onNodeWithText("Elm Street rent").assertExists()
        rule.onNodeWithText("Kept up to date by Persona").assertExists()
        rule.onNodeWithText("Checked today 08:02 · next check today 20:00").assertExists()
        rule.onNodeWithText("Refresh now").assertExists()
        rule.onNodeWithText("Pause updates").assertExists()
        rule.onNodeWithText("Alert me").assertExists().assertIsOff()
        rule.onNodeWithText("Below 2,000 USD").assertExists()
        rule.onNodeWithText("Show on Overview").assertDoesNotExist()

        // Refresh now -> Checking now… -> invalidation updates reading and clears Checking now…
        rule.onNodeWithText("Refresh now").performClick()
        rule.waitForIdle()
        assertEquals("persona.refresh", hub.sentActions.last().first)
        rule.onNodeWithText("Checking now…").assertExists()
        assertTrue("toast reports Checking now…", notices.contains("Checking now…"))

        hub.refreshing = false
        hub.tripped = true
        hub.emitInvalidate()
        rule.waitForIdle()
        rule.onNodeWithText("Checking now…").assertDoesNotExist()
        rule.onNodeWithText("1,985.5 USD").assertExists()
        rule.onNodeWithText("Below 2,000 USD · Checked today 08:02").assertExists()

        // Pause updates -> Updates paused, no Refresh now, Resume updates -> Resume restores Refresh now.
        rule.onNodeWithText("Pause updates").performClick()
        rule.waitForIdle()
        assertEquals("persona.pause", hub.sentActions.last().first)
        rule.onNodeWithText("Updates paused").assertExists()
        rule.onNodeWithText("Refresh now").assertDoesNotExist()
        rule.onNodeWithText("Resume updates").assertExists()

        rule.onNodeWithText("Resume updates").performClick()
        rule.waitForIdle()
        assertEquals("persona.resume", hub.sentActions.last().first)
        rule.onNodeWithText("Updates paused").assertDoesNotExist()
        rule.onNodeWithText("Refresh now").assertExists()
        rule.onNodeWithText("Pause updates").assertExists()

        // Alert me switch: optimistic ON before hub replies, sends persona.alert with on=true.
        val gateOn = CompletableDeferred<Unit>()
        hub.nextAlertGate = gateOn
        rule.onNodeWithText("Alert me").performClick()
        rule.waitForIdle()
        rule.onNodeWithText("Alert me").assertIsOn()
        assertEquals("persona.alert" to mapOf("on" to "true"), hub.sentActions.last())
        gateOn.complete(Unit)
        rule.waitForIdle()
        rule.onNodeWithText("Alert me").assertIsOn()
        assertTrue("toast reports Alerts on", notices.contains("Alerts on"))

        // Alert me switch: turns OFF, sends persona.alert with on=false.
        hub.nextAlertGate = null
        rule.onNodeWithText("Alert me").performClick()
        rule.waitForIdle()
        rule.onNodeWithText("Alert me").assertIsOff()
        assertEquals("persona.alert" to mapOf("on" to "false"), hub.sentActions.last())
        assertTrue("toast reports Alerts off", notices.contains("Alerts off"))

        // Alert me switch failure: flips ON optimistically while in flight, then rolls back to OFF with an error notice.
        val gateFail = CompletableDeferred<Unit>()
        hub.nextAlertGate = gateFail
        hub.failNextAlert = HubFailure(HubError.Server, "hub busy", status = 503)
        val noticesBeforeFail = notices.size
        rule.onNodeWithText("Alert me").performClick()
        rule.waitForIdle()
        rule.onNodeWithText("Alert me").assertIsOn()
        gateFail.complete(Unit)
        rule.waitForIdle()
        rule.onNodeWithText("Alert me").assertIsOff()
        assertTrue("failed switch reports error notice", notices.size > noticesBeforeFail)
    }

    @OptIn(ExperimentalCoroutinesApi::class)
    @Test fun settingsExtensionsSkipsReservedOverviewCards() = runTest {
        val pagesCard = ExtensionUiPlace("-pages", "g_1a2b3c4d5e6f7081", "Elm Street rent", "overview_card",
            extensionName = "Persona", size = "compact", opens = "pg_1a2b3c4d5e6f7081")
        val otherReservedCard = ExtensionUiPlace("-internal", "g_fedcba9876543210", "Internal card", "overview_card", size = "compact")
        val realCard = ExtensionUiPlace("example-gateway", "today", "Today's usage", "overview_card",
            extensionName = "Example Gateway", size = "compact", opens = "usage")
        val dashboardsNav = ExtensionUiPlace("-pages", "index", "Dashboards", "nav_page",
            extensionName = "Persona", size = "full", icon = "chart")
        val pinnedWatchNav = ExtensionUiPlace("-pages", "pg_1a2b3c4d5e6f7081", "Elm Street rent", "nav_page",
            extensionName = "Persona", size = "full", icon = "money")

        // Unit level: ExtensionSlotText.known and ExtensionSlotsState.cards skip overview_card for `-*`.
        assertFalse(ExtensionSlotText.known(pagesCard))
        assertFalse(ExtensionSlotText.known(otherReservedCard))
        assertTrue(ExtensionSlotText.known(realCard))
        assertTrue(ExtensionSlotText.known(dashboardsNav))
        assertTrue(ExtensionSlotText.known(pinnedWatchNav))

        val directState = ExtensionSlotsState(available = true, places = listOf(pagesCard, otherReservedCard, realCard, dashboardsNav))
        assertEquals(listOf("example-gateway" to "today"), directState.cards.map { it.extension to it.id })
        assertEquals(listOf("-pages" to "index"), directState.navPages.map { it.extension to it.id })

        // Slots controller: loading places from a hub that lists `-pages` overview_card leaves it out.
        val rendered = mutableListOf<Pair<String, String>>()
        val api = object : ExtensionApiStub() {
            override val slotsAvailable = flowOf(true)
            override suspend fun listing() = ExtensionListing(emptyList())
            override suspend fun extensions() = emptyList<HubExtension>()
            override suspend fun uiPlaces(placement: String, launcher: String?, machine: String?) =
                if (placement.isEmpty()) listOf(pagesCard, otherReservedCard, realCard, dashboardsNav) else emptyList()
            override suspend fun renderUi(extension: String, ui: String, context: ExtensionUiContext?, state: String?): ExtensionView {
                rendered += extension to ui
                return ExtensionView(title = "Today's usage", sections = listOf(ExtensionViewSection(items = listOf(
                    ExtensionViewItem("metric", label = "Requests", value = "42")))))
            }
        }
        val slots = ExtensionSlots(api, MutableStateFlow(true), MutableSharedFlow<ExtensionUiEvent>())
        backgroundScope.launch { slots.run(MutableStateFlow(true)) }
        runCurrent()
        assertEquals(listOf("example-gateway" to "today"), slots.state.value.cards.map { it.extension to it.id })
        assertEquals(listOf("-pages" to "index"), slots.state.value.navPages.map { it.extension to it.id })

        // UI level: rememberExtensionCards + extensionSlotItems in ExtensionsPage never renders `-pages` overview cards.
        val handle = ExtensionViewHandle({ _, _ -> }, { _, _ -> }, {}, {})
        var directOnlyReserved by mutableStateOf(false)
        rule.setContent {
            PersonaTheme(reduceMotion = true) {
                if (directOnlyReserved) {
                    val onlyReservedState = ExtensionSlotsState(available = true, places = listOf(pagesCard))
                    val injectedCard = ExtensionCardSlot(pagesCard, ExtensionViewState(ExtensionView(title = "Leaked card")), handle) {}
                    Box(Modifier.fillMaxSize()) {
                        LazyColumn(Modifier.fillMaxSize()) {
                            extensionSlotItems(onlyReservedState, listOf(injectedCard), selected = { false }, onOpen = {})
                        }
                    }
                } else {
                    val state by slots.state.collectAsState()
                    val cards = rememberExtensionCards(slots)
                    ExtensionsPage(api, onBack = {}, onExtension = { _, _ -> }, slots = {
                        extensionSlotItems(state, cards, selected = { false }, onOpen = {})
                    })
                }
            }
        }
        rule.waitForIdle()
        rule.onNodeWithText("Today's usage").assertExists()
        rule.onNodeWithText("42").assertExists()
        rule.onNodeWithText("Elm Street rent").assertDoesNotExist()
        rule.onNodeWithText("Internal card").assertDoesNotExist()
        assertEquals("only the real extension's overview_card is rendered", listOf("example-gateway" to "today"), rendered)

        // Even if an ExtensionCardSlot for `-pages` is passed straight to extensionSlotItems, it is skipped.
        rule.runOnIdle { directOnlyReserved = true }
        rule.waitForIdle()
        rule.onNodeWithText("Leaked card").assertDoesNotExist()
        rule.onNodeWithText("Elm Street rent").assertDoesNotExist()
    }

    @Test fun sidebarNavIconsSupportWatchPageIcons() {
        assertSame(PhIcons.Coins, extensionNavIcon("money"))
        assertSame(PhIcons.WifiHigh, extensionNavIcon("wifi"))
        assertSame(PhIcons.List, extensionNavIcon("list"))
        assertSame(PhIcons.TrendUp, extensionNavIcon("trend_up"))
        assertSame(PhIcons.Heart, extensionNavIcon("heart"))
        assertSame(PhIcons.MapPin, extensionNavIcon("location"))
        assertSame(PhIcons.ChartBar, extensionNavIcon("chart"))
        assertSame(PhIcons.CalendarBlank, extensionNavIcon("calendar"))
        assertSame(PhIcons.PuzzlePiece, extensionNavIcon("plugin"))
        assertSame(PhIcons.PuzzlePiece, extensionNavIcon("rocket"))
        assertSame(PhIcons.PuzzlePiece, extensionNavIcon(null))
    }
}
