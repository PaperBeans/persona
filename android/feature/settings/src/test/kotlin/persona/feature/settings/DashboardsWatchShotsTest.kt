package persona.feature.settings

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onFirst
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.decodeFromJsonElement
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.JsonCodec
import persona.core.contracts.api.*
import persona.core.designsystem.*
import java.io.File

/**
 * Dashboards phase 2 watch pages (#13020) drawn through the real `ExtensionUiPage` on the JVM (Robolectric native
 * graphics, no emulator):
 * - all four watch displays (`number`, `status`, `table`, `trend`) in `empty`, `normal`, and `alert`, light and dark,
 *   phone (400dp) and tablet (840dp) widths, in all three styles (Glass for empty/normal/alert; Brief and Board for
 *   normal and alert),
 * - the Dashboards index with watch rows (value, spark, icon, icon_color) in normal and alert states,
 * - footer states (default with Refresh now / Pause updates / Alert me off, refreshing with Checking now… / Alert me on,
 *   paused with Updates paused / Resume updates),
 * - Simplified Chinese and Japanese captures.
 * PNGs are written to `build/dashboards-watch-shots/` (or `-Dpersona.shots=<dir>`).
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class DashboardsWatchShotsTest {
    @get:Rule val rule = createComposeRule()

    private val fixtureDraws: Map<String, JsonObject> by lazy {
        val file = File(requireNotNull(System.getProperty("persona.repo.root")), "spec/fixtures/dashboards/watch.json")
        JsonCodec.parseToJsonElement(file.readText()).jsonObject.getValue("draws").jsonArray.associate {
            val obj = it.jsonObject
            obj.getValue("name").jsonPrimitive.content to obj
        }
    }

    private val pageIds = mapOf(
        "number" to "pg_1a2b3c4d5e6f7081",
        "status" to "pg_2b3c4d5e6f708192",
        "table" to "pg_3c4d5e6f708192a3",
        "trend" to "pg_4d5e6f708192a3b4",
    )

    private val titlesEn = mapOf(
        "number" to "Elm Street rent",
        "status" to "Shop sites",
        "table" to "Elm Street listings",
        "trend" to "Morning weight",
    )

    private val rulesEn = mapOf(
        "number" to "Below 2,000 USD",
        "status" to "Problem",
        "table" to "3 rows or more",
        "trend" to "Above 80 kg",
    )

    private val icons = mapOf(
        "number" to "money",
        "status" to "wifi",
        "table" to "list",
        "trend" to "trend_up",
    )

    private fun out(): File = File(System.getProperty("persona.shots") ?: "build/dashboards-watch-shots").also { it.mkdirs() }

    private fun watchSection(display: String, state: String): ExtensionViewSection =
        JsonCodec.decodeFromJsonElement(ExtensionViewSection.serializer(), fixtureDraws.getValue("${display}_$state").getValue("section"))

    private fun watchGlanceRow(display: String, state: String, paused: Boolean = false): ExtensionViewItem {
        val glance = fixtureDraws.getValue("${display}_$state").getValue("glance_row").jsonObject
        val subtitle = when {
            paused -> "Updates paused"
            state == "empty" -> "Not checked yet"
            else -> "Checked today 08:02"
        }
        return ExtensionViewItem(
            type = "row",
            key = "page:${pageIds.getValue(display)}",
            title = titlesEn.getValue(display),
            subtitle = subtitle,
            icon = glance["icon"]?.jsonPrimitive?.content ?: icons.getValue(display),
            iconColor = glance["icon_color"]?.jsonPrimitive?.content,
            value = glance["value"]?.jsonPrimitive?.content,
            spark = glance["spark"]?.let { JsonCodec.decodeFromJsonElement(ExtensionSpark.serializer(), it) },
            action = ExtensionViewAction("open", push = ExtensionViewPush(ui = pageIds.getValue(display))),
            actions = listOf(
                ExtensionRowAction("Pin to sidebar", icon = "star", swipe = true, action = ExtensionViewAction("persona.pin")),
                ExtensionRowAction("Remove", icon = "trash", role = "destructive", swipe = true, action = ExtensionViewAction("persona.remove")),
            ),
        )
    }

    private fun watchFooterEn(
        ruleText: String,
        empty: Boolean = false,
        refreshing: Boolean = false,
        paused: Boolean = false,
        alertOn: Boolean = false,
        pinned: Boolean = false,
    ): ExtensionViewSection {
        val updatedLine = when {
            paused && empty -> "Not checked yet"
            paused -> "Checked today 08:02"
            empty -> "Not checked yet · next check today 20:00"
            else -> "Checked today 08:02 · next check today 20:00"
        }
        val items = buildList {
            add(ExtensionViewItem("text", key = "persona.updated", text = updatedLine, style = "caption"))
            if (paused) add(ExtensionViewItem("text", key = "persona.paused", text = "Updates paused", style = "caption"))
            if (refreshing) add(ExtensionViewItem("text", key = "persona.status", text = "Checking now…", style = "caption"))
            if (!paused) add(ExtensionViewItem("button", key = "persona.refresh", title = "Refresh now", action = ExtensionViewAction("persona.refresh")))
            add(ExtensionViewItem("button", key = "persona.pause", title = if (paused) "Resume updates" else "Pause updates",
                action = ExtensionViewAction(if (paused) "persona.resume" else "persona.pause")))
            add(ExtensionViewItem("row", key = "persona.alert", title = "Alert me", subtitle = ruleText,
                control = ExtensionRowControl("switch", "on", on = alertOn, action = ExtensionViewAction("persona.alert"))))
            add(ExtensionViewItem("button", key = "persona.pin", title = if (pinned) "Unpin from sidebar" else "Pin to sidebar",
                action = ExtensionViewAction(if (pinned) "persona.unpin" else "persona.pin")))
            add(ExtensionViewItem("button", key = "persona.rename", title = "Rename",
                action = ExtensionViewAction("persona.rename", push = ExtensionViewPush(state = "rename", title = "Rename"))))
            add(ExtensionViewItem("button", key = "persona.remove", title = "Remove", role = "destructive",
                action = ExtensionViewAction("persona.remove", confirm = "Remove this page?")))
        }
        return ExtensionViewSection(key = "persona.footer", items = items)
    }

    private fun watchPageEn(display: String, state: String, refreshing: Boolean = false, paused: Boolean = false, alertOn: Boolean = false): ExtensionView {
        val sections = buildList {
            add(watchSection(display, state))
            if (display == "number") {
                add(ExtensionViewSection(key = "notes", items = listOf(
                    ExtensionViewItem("text", key = "notes.criteria", text = "1-bedroom listings within 10 minutes of Elm Street station.", style = "secondary"))))
            }
            add(watchFooterEn(rulesEn.getValue(display), empty = state == "empty", refreshing = refreshing, paused = paused, alertOn = alertOn, pinned = display == "number"))
        }
        return ExtensionView(title = titlesEn.getValue(display), sections = sections)
    }

    private fun indexViewEn(state: String): ExtensionView = ExtensionView(
        title = "Dashboards",
        sections = listOf(
            ExtensionViewSection(key = "pinned", title = "Pinned", items = listOf(
                watchGlanceRow("number", state),
                ExtensionViewItem("row", key = "page:pg_3f9a1c0d5e7b2a41", title = "Morning run", subtitle = "Updated today 8:02", icon = "heart",
                    action = ExtensionViewAction("open", push = ExtensionViewPush(ui = "pg_3f9a1c0d5e7b2a41")),
                    actions = listOf(ExtensionRowAction("Unpin from sidebar", icon = "star", swipe = true, action = ExtensionViewAction("persona.unpin")))),
            )),
            ExtensionViewSection(key = "all", title = "All", items = listOf(
                watchGlanceRow("status", state),
                watchGlanceRow("table", state),
                watchGlanceRow("trend", state),
            )),
        ),
    )

    private class ViewsApi(private val views: Map<String, ExtensionView>) : ExtensionApiStub() {
        override suspend fun renderUi(extension: String, ui: String, context: ExtensionUiContext?, state: String?): ExtensionView =
            views.getValue(ui)
    }

    private fun capture(name: String) {
        rule.waitForIdle()
        val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
        val bg = bitmap.getPixel(1, 1)
        var drawn = 0
        for (y in 0 until bitmap.height step 9) for (x in 0 until bitmap.width step 9) if (bitmap.getPixel(x, y) != bg) drawn++
        assertTrue("$name drew nothing", drawn > 80)
        File(out(), "$name.png").outputStream().use { assertTrue(bitmap.compress(Bitmap.CompressFormat.PNG, 100, it)) }
    }

    private fun captureEnglishWidth(widthTag: String, width: Dp, height: Dp) {
        val views = linkedMapOf<String, ExtensionView>()
        val displays = listOf("number", "status", "table", "trend")
        val states = listOf("empty", "normal", "alert")
        for (d in displays) for (s in states) views["pg_${d}_$s"] = watchPageEn(d, s)
        views["pg_footer_refreshing"] = watchPageEn("number", "normal", refreshing = true, alertOn = true)
        views["pg_footer_paused"] = watchPageEn("number", "normal", paused = true, alertOn = false)
        views[DashboardPages.INDEX] = indexViewEn("normal")
        // Separate index view for alert rows, rendered under `-pages` / `index` by swapping the map value.
        val dynamicViews = views.toMutableMap()
        val api = object : ExtensionApiStub() {
            override suspend fun renderUi(extension: String, ui: String, context: ExtensionUiContext?, state: String?) = dynamicViews.getValue(ui)
        }

        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        var ui by mutableStateOf("pg_number_empty")
        var stateParam by mutableStateOf<String?>(null)
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                Box(Modifier.testTag("shot").width(width).height(height).background(PersonaTheme.palette.background)) {
                    ExtensionUiPage(api, DashboardPages.EXTENSION, ui, "", initialState = stateParam, onBack = {})
                }
            }
        }

        // Four displays × empty/normal/alert: Glass for all three states; Brief and Board for normal & alert.
        for (d in displays) for (s in states) {
            ui = "pg_${d}_$s"; stateParam = null
            rule.waitForIdle()
            rule.onAllNodesWithText(titlesEn.getValue(d)).onFirst().assertExists()
            rule.onNodeWithText("Kept up to date by Persona").assertExists()
            val stylesForState = if (s == "empty") listOf(AppStyle.Glass) else AppStyle.entries
            for (st in stylesForState) for (dk in listOf(false, true)) {
                style = st; dark = dk
                capture("watch-$d-$s-$widthTag-${st.name.lowercase()}-${if (dk) "dark" else "light"}")
            }
        }

        // Footer states (refreshing + alert on; paused + Updates paused + Resume updates) in all 3 styles, light & dark.
        for (footerKey in listOf("refreshing", "paused")) {
            ui = "pg_footer_$footerKey"; stateParam = null
            rule.waitForIdle()
            if (footerKey == "refreshing") rule.onNodeWithText("Checking now…").assertExists()
            else rule.onNodeWithText("Updates paused").assertExists()
            for (st in AppStyle.entries) for (dk in listOf(false, true)) {
                style = st; dark = dk
                capture("watch-footer-$footerKey-$widthTag-${st.name.lowercase()}-${if (dk) "dark" else "light"}")
            }
        }

        // Dashboards index (normal and alert glance rows with value, spark, icon, icon_color) in all 3 styles, light & dark.
        for (indexState in listOf("normal", "alert")) {
            dynamicViews[DashboardPages.INDEX] = indexViewEn(indexState)
            ui = DashboardPages.INDEX; stateParam = indexState
            rule.waitForIdle()
            rule.onNodeWithText("Dashboards").assertExists()
            rule.onNodeWithText("Kept up to date by Persona").assertDoesNotExist()
            for (st in AppStyle.entries) for (dk in listOf(false, true)) {
                style = st; dark = dk
                capture("watch-index-$indexState-$widthTag-${st.name.lowercase()}-${if (dk) "dark" else "light"}")
            }
        }
    }

    @Config(sdk = [35], qualifiers = "en-w400dp-h920dp-xhdpi")
    @Test fun englishPhone() = captureEnglishWidth("phone", 400.dp, 920.dp)

    @Config(sdk = [35], qualifiers = "en-w840dp-h920dp-xhdpi")
    @Test fun englishTablet() = captureEnglishWidth("tablet", 840.dp, 920.dp)

    @Config(sdk = [35], qualifiers = "zh-w400dp-h920dp-xhdpi")
    @Test fun simplifiedChineseWatch() {
        val pageId = pageIds.getValue("trend")
        val view = ExtensionView(
            title = "晨间体重", // i18n-ignore: synthetic hub content
            sections = listOf(
                ExtensionViewSection(key = "persona.watch", items = listOf(
                    ExtensionViewItem("metric", key = "persona.reading", label = "晨间体重", value = "80.3 公斤", // i18n-ignore: synthetic hub content
                        detail = "高于 80 公斤 · 今天 08:02 检查", tone = "caution", icon = "warning", // i18n-ignore: synthetic hub content
                        spark = ExtensionSpark(listOf(78.2, 78.0, 79.4, 80.3), tone = "caution", fill = true)),
                )),
                ExtensionViewSection(key = "persona.footer", items = listOf(
                    ExtensionViewItem("text", key = "persona.updated", text = "今天 08:02 检查 · 下次检查今天 20:00", style = "caption"), // i18n-ignore: synthetic hub content
                    ExtensionViewItem("button", key = "persona.refresh", title = "立即刷新", action = ExtensionViewAction("persona.refresh")), // i18n-ignore: synthetic hub content
                    ExtensionViewItem("button", key = "persona.pause", title = "暂停更新", action = ExtensionViewAction("persona.pause")), // i18n-ignore: synthetic hub content
                    ExtensionViewItem("row", key = "persona.alert", title = "触发时提醒", subtitle = "高于 80 公斤", // i18n-ignore: synthetic hub content
                        control = ExtensionRowControl("switch", "on", on = true, action = ExtensionViewAction("persona.alert"))),
                    ExtensionViewItem("button", key = "persona.pin", title = "固定到侧边栏", action = ExtensionViewAction("persona.pin")), // i18n-ignore: synthetic hub content
                    ExtensionViewItem("button", key = "persona.rename", title = "改名", action = ExtensionViewAction("persona.rename")), // i18n-ignore: synthetic hub content
                    ExtensionViewItem("button", key = "persona.remove", title = "移除", role = "destructive", action = ExtensionViewAction("persona.remove")), // i18n-ignore: synthetic hub content
                )),
            ),
        )
        val api = ViewsApi(mapOf(pageId to view))
        var dark by mutableStateOf(false)
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = AppStyle.Glass) {
                Box(Modifier.testTag("shot").width(400.dp).height(920.dp).background(PersonaTheme.palette.background)) {
                    ExtensionUiPage(api, DashboardPages.EXTENSION, pageId, "", onBack = {})
                }
            }
        }
        rule.waitForIdle()
        rule.onNodeWithText("由 Persona 维护").assertExists() // i18n-ignore: ownership line check in zh-Hans
        for (dk in listOf(false, true)) {
            dark = dk
            capture("watch-trend-alert-zh-phone-glass-${if (dk) "dark" else "light"}")
        }
    }

    @Config(sdk = [35], qualifiers = "ja-w400dp-h920dp-xhdpi")
    @Test fun japaneseWatch() {
        val pageId = pageIds.getValue("table")
        val view = ExtensionView(
            title = "新着の賃貸物件", // i18n-ignore: synthetic hub content
            sections = listOf(
                ExtensionViewSection(key = "persona.watch", items = listOf(
                    ExtensionViewItem("metric", key = "persona.reading", label = "2,000 ドル未満の新着 1 ベッドルーム物件", value = "3 件", // i18n-ignore: synthetic hub content
                        detail = "3 件以上 · 今日 08:02 に確認", tone = "caution", icon = "warning"), // i18n-ignore: synthetic hub content
                    ExtensionViewItem("table", key = "persona.rows",
                        columns = listOf(ExtensionTableColumn("物件"), ExtensionTableColumn("家賃")), // i18n-ignore: synthetic hub content
                        rows = listOf(
                            listOf("エルム通り 12 番 2 階", "1,950"), // i18n-ignore: synthetic hub content
                            listOf("オーク通り 40 番", "1,880"), // i18n-ignore: synthetic hub content
                            listOf("バーチ通り 7 番", "1,990"), // i18n-ignore: synthetic hub content
                        )),
                )),
                ExtensionViewSection(key = "persona.footer", items = listOf(
                    ExtensionViewItem("text", key = "persona.updated", text = "今日 08:02 に確認 · 次回確認 今日 20:00", style = "caption"), // i18n-ignore: synthetic hub content
                    ExtensionViewItem("button", key = "persona.refresh", title = "今すぐ更新", action = ExtensionViewAction("persona.refresh")), // i18n-ignore: synthetic hub content
                    ExtensionViewItem("button", key = "persona.pause", title = "更新を一時停止", action = ExtensionViewAction("persona.pause")), // i18n-ignore: synthetic hub content
                    ExtensionViewItem("row", key = "persona.alert", title = "条件に達したら通知", subtitle = "3 件以上", // i18n-ignore: synthetic hub content
                        control = ExtensionRowControl("switch", "on", on = false, action = ExtensionViewAction("persona.alert"))),
                    ExtensionViewItem("button", key = "persona.pin", title = "サイドバーに固定", action = ExtensionViewAction("persona.pin")), // i18n-ignore: synthetic hub content
                    ExtensionViewItem("button", key = "persona.rename", title = "名前を変更", action = ExtensionViewAction("persona.rename")), // i18n-ignore: synthetic hub content
                    ExtensionViewItem("button", key = "persona.remove", title = "削除", role = "destructive", action = ExtensionViewAction("persona.remove")), // i18n-ignore: synthetic hub content
                )),
            ),
        )
        val api = ViewsApi(mapOf(pageId to view))
        var dark by mutableStateOf(false)
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = AppStyle.Glass) {
                Box(Modifier.testTag("shot").width(400.dp).height(920.dp).background(PersonaTheme.palette.background)) {
                    ExtensionUiPage(api, DashboardPages.EXTENSION, pageId, "", onBack = {})
                }
            }
        }
        rule.waitForIdle()
        rule.onNodeWithText("Persona が更新しています").assertExists() // i18n-ignore: ownership line check in ja
        for (dk in listOf(false, true)) {
            dark = dk
            capture("watch-table-alert-ja-phone-glass-${if (dk) "dark" else "light"}")
        }
    }
}
