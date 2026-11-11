package persona.feature.settings

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.flowOf
import kotlinx.serialization.json.*
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.Machine
import persona.core.contracts.MachineComputer
import persona.core.navigation.LocalDetailKey
import persona.core.navigation.ExtensionRoute
import persona.core.contracts.api.*
import persona.core.designsystem.*
import java.io.File

/**
 * A market extension's page aligned with the web (#3049): its "Upgrades" group (auto-upgrade switch, the newer version waiting
 * for a yes), a machine list drawn as a switch per Mac, "Remove" at the end with "Keep data" ticked; and "Manage markets"' switch for all.
 * Every style, light and dark; the PNGs are in build/extension-upgrade-shots.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "zh-w400dp-h2200dp-xhdpi")
class ExtensionUpgradeShotsTest {
    @get:Rule val rule = createComposeRule()

    private val machinesField = ExtensionField("machines", "array", title = "在哪些 Mac 上用",
        items = ExtensionItems("string", format = "machine", os = listOf("darwin")), confirm = "扩展开着时这台 Mac 不会自动锁屏。")
    private val detail = ExtensionDetail(
        HubExtension("example-cua", "示例操控", "computer", "running", version = "1.0.0", source = "installed", enabled = true,
            contributes = mapOf("computer" to listOf("example-cua")), autoUpgrade = true,
            upgrade = ExtensionUpgrade("example-cua", market = "m1", from = "1.0.0", to = "1.2.0", state = "needs_permission",
                addedPermissions = listOf(ExtensionPermission("net", "访问更多网络", "api.example.com"))),
            nodes = listOf(ExtensionNodeState("mac-a", version = "1.0.0", state = "running"))),
        origin = "market:m1/example-cua", publisher = "Example",
        form = listOf(machinesField), config = buildJsonObject { putJsonArray("machines") { add("mac-a") } },
        nodeMachines = "machines",
    )
    private val machines = listOf(
        Machine("mac-a", name = "工作 Mac", os = "darwin", online = true),
        Machine("mac-b", name = "家里 Mac", os = "darwin", online = false),
        Machine("mac-c", name = "会议室 Mac", os = "darwin", online = true,
            computer = MachineComputer(enabled = true, installed = true, daemon = true, accessibility = "granted", screenRecording = "granted", desktop = "locked", note = "解锁屏幕")),
        Machine("box", name = "Linux 机器", os = "linux", online = true),
    )

    private fun capture(name: String, node: SemanticsNodeInteraction = rule.onNodeWithTag("shot")) {
        rule.waitForIdle()
        val image = node.captureToImage().asAndroidBitmap()
        assertTrue(image.width > 0 && image.height > 0)
        val dir = File("build/extension-upgrade-shots").also { it.mkdirs() }
        File(dir, "$name.png").outputStream().use { image.compress(Bitmap.CompressFormat.PNG, 100, it) }
    }

    @Test fun marketExtensionPageInEveryStyle() {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        var current = detail
        val configs = mutableListOf<JsonObject>()
        val removed = mutableListOf<Boolean>()
        var upgradeOpened: Pair<String, String>? = null
        val api = object : ExtensionApiStub() {
            override suspend fun extension(id: String) = current
            override suspend fun setConfig(id: String, config: JsonObject): ExtensionDetail { configs += config; current = current.copy(config = config); return current }
            override suspend fun uninstall(id: String, keepData: Boolean, cascade: Boolean, members: Boolean): ExtensionRemoval { removed += keepData; return ExtensionRemoval() }
        }
        rule.setContent {
            PersonaTheme(style = style, darkTheme = dark, reduceMotion = true) {
                Box(Modifier.testTag("shot").fillMaxSize().background(PersonaTheme.palette.background)) {
                    ExtensionPage(api, "example-cua", onBack = {}, machines = flowOf(machines), onUpgrade = { m, id -> upgradeOpened = m to id })
                }
            }
        }
        rule.waitUntil { rule.onAllNodesWithText("家里 Mac").fetchSemanticsNodes().isNotEmpty() }
        // Only Macs; the switched-on one says how the program is there.
        rule.onNodeWithText("Linux 机器").assertDoesNotExist()
        rule.onNodeWithText("在线 · 程序在运行 · 1.0.0").assertExists()
        // An extension that operates computers: a Mac not running its program says how Cua Driver is there.
        rule.onNodeWithText("在线 · 锁屏了").assertExists()
        rule.onNodeWithText("有新版本 1.2.0").assertExists()
        rule.onNodeWithText("要多 1 项权限：访问更多网络（api.example.com）").assertExists()
        for (s in AppStyle.entries) for (d in listOf(false, true)) {
            style = s; dark = d
            capture("detail-${s.key}-${if (d) "dark" else "light"}")
        }
        style = AppStyle.Glass; dark = false

        rule.onNodeWithText("有新版本 1.2.0").performClick()
        assertEquals("m1" to "example-cua", upgradeOpened)

        // x-confirm asks before a Mac is turned on; nothing is saved until the yes.
        // A dialog is a window of its own: each style opens it anew.
        for (s in AppStyle.entries) for (d in listOf(false, true)) {
            style = s; dark = d
            rule.waitForIdle()
            rule.onNodeWithText("家里 Mac").performScrollTo().performClick()
            capture("confirm-machine-${s.key}-${if (d) "dark" else "light"}", rule.onNode(isDialog()))
            rule.onNodeWithText("取消").performClick()
        }
        assertEquals(0, configs.size)
        style = AppStyle.Glass; dark = false
        rule.waitForIdle()
        rule.onNodeWithText("家里 Mac").performScrollTo().performClick()
        rule.onNodeWithText("打开").performClick()
        rule.waitUntil { configs.isNotEmpty() }
        assertEquals(buildJsonObject { putJsonArray("machines") { add("mac-a"); add("mac-b") } }, configs.single())

        // "Remove" keeps the data unless unticked.
        for (s in AppStyle.entries) for (d in listOf(false, true)) {
            style = s; dark = d
            rule.waitForIdle()
            rule.onNodeWithText("卸载").performScrollTo().performClick()
            capture("uninstall-${s.key}-${if (d) "dark" else "light"}", rule.onNode(isDialog()))
            rule.onNodeWithText("取消").performClick()
        }
        assertEquals(0, removed.size)
        rule.waitForIdle()
        rule.onNodeWithText("卸载").performScrollTo().performClick()
        rule.onAllNodesWithText("卸载").filterToOne(hasAnyAncestor(isDialog()) and hasClickAction()).performClick()
        rule.waitUntil { removed.isNotEmpty() }
        assertEquals(listOf(true), removed)
    }

    /** A wide window keeps the list beside the page: removing the extension there takes its row off the list at once. */
    @Test fun removingBesideTheListTakesItsRowOff() {
        var installed = listOf(detail.extension, HubExtension("other", "另一个扩展", "ui", "running"))
        var open by mutableStateOf(true)
        val api = object : ExtensionApiStub() {
            override suspend fun extensions() = installed
            override suspend fun extension(id: String) = detail
            override suspend fun uninstall(id: String, keepData: Boolean, cascade: Boolean, members: Boolean): ExtensionRemoval { installed = installed.filter { it.id != id }; return ExtensionRemoval() }
        }
        rule.setContent {
            PersonaTheme(style = AppStyle.Glass, darkTheme = false, reduceMotion = true) {
                CompositionLocalProvider(LocalDetailKey provides if (open) ExtensionRoute("example-cua") else null) {
                    Row(Modifier.fillMaxSize().background(PersonaTheme.palette.background)) {
                        Box(Modifier.weight(1f).testTag("list")) { ExtensionsPage(api, onBack = {}, onExtension = { _, _ -> }) }
                        if (open) Box(Modifier.weight(1f).testTag("detail")) {
                            ExtensionPage(api, "example-cua", onBack = { open = false }, machines = flowOf(machines))
                        }
                    }
                }
            }
        }
        rule.waitUntil { rule.onAllNodesWithText("另一个扩展").fetchSemanticsNodes().isNotEmpty() }
        rule.onNode(hasText("示例操控") and hasAnyAncestor(hasTestTag("list"))).assertExists()
        rule.onNode(hasText("卸载") and hasAnyAncestor(hasTestTag("detail"))).performScrollTo().performClick()
        rule.onAllNodesWithText("卸载").filterToOne(hasAnyAncestor(isDialog()) and hasClickAction()).performClick()
        rule.waitUntil(5_000) { rule.onAllNodes(hasText("示例操控") and hasAnyAncestor(hasTestTag("list"))).fetchSemanticsNodes().isEmpty() }
        rule.onNodeWithText("另一个扩展").assertExists()
    }

    @Test fun marketsPageAutoUpgradeSwitchInEveryStyle() {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        val api = object : ExtensionApiStub() {
            override suspend fun markets() = listOf(ExtensionMarket("o1", "github:example/official", "官方扩展", extensions = 2, official = true))
            override suspend fun autoUpgrades() = true
        }
        rule.setContent {
            PersonaTheme(style = style, darkTheme = dark, reduceMotion = true) {
                Box(Modifier.testTag("shot").fillMaxSize().background(PersonaTheme.palette.background)) { ExtensionMarketsPage(api, {}) }
            }
        }
        rule.waitUntil { rule.onAllNodesWithText("自动升级扩展").fetchSemanticsNodes().isNotEmpty() }
        for (s in AppStyle.entries) for (d in listOf(false, true)) {
            style = s; dark = d
            capture("markets-auto-${s.key}-${if (d) "dark" else "light"}")
        }
    }
}
