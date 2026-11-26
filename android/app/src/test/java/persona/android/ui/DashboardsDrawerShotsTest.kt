package persona.android.ui

import android.app.Application
import android.content.ComponentName
import android.graphics.Bitmap
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createEmptyComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.unit.dp
import androidx.test.core.app.ActivityScenario
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.flowOf
import kotlinx.serialization.json.JsonObject
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Shadows
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.android.navigation.extensionSidebarKey
import persona.core.contracts.api.*
import persona.core.designsystem.AppStyle
import persona.core.designsystem.NavItem
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.icons.PhIcons
import persona.feature.assistant.AssistantSidebarContent
import persona.feature.settings.extensionNavIcon
import persona.feature.settings.rememberExtensionSlots
import java.io.File

/**
 * The real Persona drawer (`AssistantSidebarContent`) with the rows the shell builds from the hub's extension places
 * (`rememberExtensionSlots`, mapped as `Root.kt`'s `sidebarPages`), for Dashboards (#15017): with the switch off or no
 * page kept the hub lists no `-pages` place, so the drawer must be pixel-identical to before; once a page is kept, a
 * Dashboards row and each pinned page join the extensions' rows. Every style, light and dark, in English, Simplified
 * Chinese and Japanese, written to `build/dashboards-drawer-shots/` (or `-Dpersona.shots=<dir>`).
 * Uses only APIs that exist before #15017, so the same file captures the "before" drawer on an older tree.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class DashboardsDrawerShotsTest {
    @get:Rule val rule = createEmptyComposeRule()

    private class Words(val language: String, val machines: String, val usage: String, val gateway: String,
                        val dashboards: String, val pinned: String)

    /** The hub's places list: what `GET /api/v1/extensions/-/ui` answers for each case. */
    private class Places(val places: List<ExtensionUiPlace>) : ExtensionManagement {
        override val available = flowOf(true)
        override val slotsAvailable = flowOf(true)
        override suspend fun uiPlaces(placement: String, launcher: String?, machine: String?) =
            if (placement.isEmpty()) places else emptyList()
        private fun unused(): Nothing = throw AssertionError("not used in this test")
        override suspend fun extensions(): List<HubExtension> = unused()
        override suspend fun extension(id: String): ExtensionDetail = unused()
        override suspend fun setEnabled(id: String, enabled: Boolean, cascade: Boolean): ExtensionDetail = unused()
        override suspend fun setConfig(id: String, config: JsonObject): ExtensionDetail = unused()
        override suspend fun setSecret(id: String, name: String, value: String): ExtensionDetail = unused()
        override suspend fun clearSecret(id: String, name: String): ExtensionDetail = unused()
        override suspend fun renderUi(extension: String, ui: String, context: ExtensionUiContext?, state: String?): ExtensionView = unused()
        override suspend fun uiAction(extension: String, ui: String, action: String, context: ExtensionUiContext?, state: String?,
            values: Map<String, String>?): ExtensionUiResult = unused()
        override suspend fun markets(): List<ExtensionMarket> = unused()
        override suspend fun addMarket(spec: String, token: String?): ExtensionMarket = unused()
        override suspend fun removeMarket(id: String): Unit = unused()
        override suspend fun refreshMarket(id: String): ExtensionMarket = unused()
        override suspend fun setMarketToken(id: String, token: String): ExtensionMarket = unused()
        override suspend fun catalog(query: String, market: String?, refresh: Boolean): ExtensionCatalog = unused()
        override suspend fun previewFromMarket(market: String, extension: String): ExtensionPreview = unused()
        override suspend fun install(preview: ExtensionPreview): ExtensionDetail = unused()
        override suspend fun upgrade(id: String, preview: ExtensionPreview): ExtensionDetail = unused()
        override suspend fun uninstall(id: String, keepData: Boolean, cascade: Boolean, members: Boolean): ExtensionRemoval = unused()
    }

    /** `Root.kt`'s `sidebarPages` for the extension pages, after the Machines row. */
    private fun rows(w: Words, pages: List<ExtensionUiPlace>) = listOf(NavItem("machines", w.machines, PhIcons.Desktop)) + pages.map { page ->
        NavItem(extensionSidebarKey(page.extension, page.id), page.title.ifBlank { page.id }, extensionNavIcon(page.icon),
            badge = page.badge?.text?.trim()?.toIntOrNull()?.coerceAtLeast(0) ?: 0,
            dot = page.badge?.text?.trim()?.let { it.isNotEmpty() && it.toIntOrNull() == null } == true)
    }

    private fun cases(w: Words): List<Pair<String, List<ExtensionUiPlace>>> {
        val usage = ExtensionUiPlace("example-gateway", "usage", w.usage, "nav_page", extensionName = w.gateway, icon = "chart")
        return listOf(
            // Switch off, or on with nothing kept: the hub lists no -pages place.
            "none" to emptyList(),
            "off" to listOf(usage),
            // The first page kept and one pinned.
            "kept" to listOf(usage,
                ExtensionUiPlace("-pages", "index", w.dashboards, "nav_page", extensionName = "Persona", size = "full", icon = "chart"),
                ExtensionUiPlace("-pages", "pg_0123456789abcdef", w.pinned, "nav_page", size = "full", icon = "calendar")),
        )
    }

    private fun shots(w: Words) {
        var case by mutableStateOf(0)
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        val all = cases(w)
        val operations = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
        val snapshot = AssistantSnapshot()
        val repository = object : AssistantRepository {
            override val assistant = MutableStateFlow(snapshot)
            override suspend fun refresh() = Unit
        }
        val online = MutableStateFlow(ConnectionState.Online)
        val app = ApplicationProvider.getApplicationContext<Application>()
        Shadows.shadowOf(app.packageManager).addActivityIfNotPresent(ComponentName(app, ComponentActivity::class.java))
        try {
            ActivityScenario.launch(ComponentActivity::class.java).use { scenario ->
                scenario.onActivity { activity -> activity.setContent {
                    PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                        key(case) {
                            val api = remember { Places(all[case].second) }
                            val slots = rememberExtensionSlots(api, online)
                            val pages = slots?.state?.collectAsState()?.value?.navPages.orEmpty()
                            Box(Modifier.testTag("shot").width(400.dp).height(1200.dp).background(PersonaTheme.palette.background)) {
                                AssistantSidebarContent(snapshot, repository, null, operations, demo = false,
                                    onOpen = {}, onAction = {}, onSettings = {}, onInbox = {}, onKnowledge = {},
                                    onTodos = null, todoCount = 0, showTodos = false, inboxCount = 0,
                                    settingsCount = 0, settingsDot = false, settingsBadge = null, pages = rows(w, pages),
                                    onPage = {}, onRetired = {})
                            }
                        }
                    }
                } }
                all.forEachIndexed { index, (name, places) ->
                    rule.runOnIdle { case = index }
                    places.lastOrNull()?.let { last -> rule.waitUntil(5_000) {
                        rule.onAllNodes(androidx.compose.ui.test.hasText(last.title)).fetchSemanticsNodes().isNotEmpty()
                    } }
                    rule.onNodeWithText(w.machines).assertExists()
                    for (s in AppStyle.entries) for (d in listOf(false, true)) {
                        rule.runOnIdle { style = s; dark = d }
                        rule.waitForIdle()
                        capture("drawer-$name-${w.language}-${s.name.lowercase()}-${if (d) "dark" else "light"}")
                    }
                }
            }
        } finally { operations.cancel() }
    }

    private fun capture(name: String) {
        rule.waitForIdle()
        val dir = File(System.getProperty("persona.shots") ?: "build/dashboards-drawer-shots").also { it.mkdirs() }
        File(dir, "$name.png").outputStream().use { output ->
            assertTrue(rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap().compress(Bitmap.CompressFormat.PNG, 100, output))
        }
    }

    @Config(application = Application::class, sdk = [35], qualifiers = "en-w400dp-h1200dp-xhdpi")
    @Test fun english() = shots(Words("en", "Computers", "Usage", "Example gateway", "Dashboards", "Running log"))

    @Config(application = Application::class, sdk = [35], qualifiers = "zh-w400dp-h1200dp-xhdpi")
    @Test fun simplifiedChinese() = shots(Words("zh", "电脑", "用量", "示例网关", "仪表盘", "跑步打卡")) // i18n-ignore: titles the hub would send

    @Config(application = Application::class, sdk = [35], qualifiers = "ja-w400dp-h1200dp-xhdpi")
    @Test fun japanese() = shots(Words("ja", "コンピュータ", "使用量", "サンプルゲートウェイ", "ダッシュボード", "ランニング記録")) // i18n-ignore: titles the hub would send
}
