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
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.api.*
import persona.core.designsystem.*
import java.io.File

/**
 * A Dashboards page (#15017) drawn through the ordinary extension page on the JVM (Robolectric native graphics, no
 * emulator): a pinned page with the ownership line under its header, and the index without it, in every style, light
 * and dark, in English, Simplified Chinese and Japanese. Written to `build/dashboards-shots/` (or `-Dpersona.shots=<dir>`).
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class DashboardPageShotsTest {
    @get:Rule val rule = createComposeRule()

    private class Words(val language: String, val index: String, val page: String, val owner: String,
                        val today: String, val week: String, val streak: String, val days: String, val log: String,
                        val entries: List<Pair<String, String>>, val done: String, val footer: String, val pages: List<Pair<String, String>>)

    private val pageId = "pg_0123456789abcdef"

    private fun view(w: Words) = ExtensionView(title = w.page, sections = listOf(
        ExtensionViewSection(items = listOf(ExtensionViewItem("stat_grid", stats = listOf(
            ExtensionStat(w.today, "5.2 km", tone = "positive"), ExtensionStat(w.week, "18.4 km"), ExtensionStat(w.streak, w.days))))),
        ExtensionViewSection(title = w.log, footer = w.footer, items = w.entries.map { (day, value) ->
            ExtensionViewItem("row", title = day, value = value) } + ExtensionViewItem("button", title = w.done, action = ExtensionViewAction("done"))),
    ))

    private fun index(w: Words) = ExtensionView(title = w.index, sections = listOf(
        ExtensionViewSection(items = w.pages.map { (title, subtitle) -> ExtensionViewItem("row", title = title, subtitle = subtitle) })))

    private class Pages(val views: Map<String, ExtensionView>) : ExtensionApiStub() {
        override suspend fun renderUi(extension: String, ui: String, context: ExtensionUiContext?, state: String?) = views.getValue(ui)
    }

    private fun out(): File = File(System.getProperty("persona.shots") ?: "build/dashboards-shots").also { it.mkdirs() }

    private fun shots(w: Words) {
        val api = Pages(mapOf(pageId to view(w), "index" to index(w)))
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        var ui by mutableStateOf(pageId)
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                Box(Modifier.testTag("shot").width(400.dp).height(760.dp).background(PersonaTheme.palette.background)) {
                    // Opened from a deep link: no title yet, the View names the page.
                    ExtensionUiPage(api, DashboardPages.EXTENSION, ui, "", onBack = {})
                }
            }
        }
        for (page in listOf(pageId, "index")) {
            ui = page
            rule.waitForIdle()
            rule.onNodeWithText(if (page == "index") w.index else w.page).assertExists()
            if (page == "index") rule.onNodeWithText(w.owner).assertDoesNotExist() else rule.onNodeWithText(w.owner).assertExists()
            rule.onNodeWithText(pageId).assertDoesNotExist()
            for (s in AppStyle.entries) for (d in listOf(false, true)) {
                style = s; dark = d
                rule.waitForIdle()
                val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
                val name = "dashboards-${if (page == "index") "index" else "page"}-${w.language}-${s.name.lowercase()}-${if (d) "dark" else "light"}"
                File(out(), "$name.png").outputStream().use { assertTrue(bitmap.compress(Bitmap.CompressFormat.PNG, 100, it)) }
            }
        }
    }

    @Config(sdk = [35], qualifiers = "en-w400dp-h760dp-xhdpi")
    @Test fun english() = shots(Words("en", "Dashboards", "Running log", "Kept up to date by Persona",
        "Today", "This week", "Streak", "4 days", "Recent runs",
        listOf("Mon" to "5.2 km", "Sun" to "6.0 km", "Sat" to "3.1 km"), "Done today",
        "Tap Done today after a run; Persona adds it here.", listOf("Running log" to "Pinned", "Reading list" to "Updated today")))

    @Config(sdk = [35], qualifiers = "zh-w400dp-h760dp-xhdpi")
    @Test fun simplifiedChinese() = shots(Words("zh", "仪表盘", "跑步打卡", "由 Persona 维护",
        "今天", "本周", "连续", "4 天", "最近几次", // i18n-ignore: synthetic page content the hub would send
        listOf("周一" to "5.2 公里", "周日" to "6.0 公里", "周六" to "3.1 公里"), "今天完成", // i18n-ignore: synthetic page content
        "跑完点「今天完成」，Persona 会记在这里。", listOf("跑步打卡" to "已固定", "阅读清单" to "今天更新"))) // i18n-ignore: synthetic page content

    @Config(sdk = [35], qualifiers = "ja-w400dp-h760dp-xhdpi")
    @Test fun japanese() = shots(Words("ja", "ダッシュボード", "ランニング記録", "Persona が更新しています",
        "今日", "今週", "連続", "4 日", "最近の記録", // i18n-ignore: synthetic page content the hub would send
        listOf("月" to "5.2 km", "日" to "6.0 km", "土" to "3.1 km"), "今日は完了", // i18n-ignore: synthetic page content
        "走ったら「今日は完了」をタップすると Persona がここに記録します。", listOf("ランニング記録" to "固定済み", "読書リスト" to "今日更新"))) // i18n-ignore: synthetic page content
}
