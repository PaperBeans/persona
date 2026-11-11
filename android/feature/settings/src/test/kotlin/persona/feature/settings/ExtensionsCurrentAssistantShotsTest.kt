package persona.feature.settings

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.isSelected
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.api.AssistantManagerChoice
import persona.core.contracts.api.ExtensionListing
import persona.core.contracts.api.HubExtension
import persona.core.contracts.api.HubManager
import persona.core.contracts.api.ManagerRoster
import persona.core.designsystem.AppStyle
import persona.core.designsystem.LocalManagerRoster
import persona.core.designsystem.PersonaTheme
import persona.core.navigation.ExtensionRoute
import persona.core.navigation.LocalDetailKey
import java.io.File
import java.time.Instant

/**
 * 设置 › 扩展 is the current assistant's (docs/assistant/managers.md "Clients"): its extensions only, in one list with no
 * assistant headings (the unstamped ones are the default's); another assistant current, that one's. Written to
 * `build/managers-shots/plugins-*.png`.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "zh-w400dp-h900dp-xhdpi")
class ExtensionsCurrentAssistantShotsTest {
    @get:Rule val rule = createComposeRule()
    @After fun reset() = AssistantManagerChoice.reset()

    private val roster = ManagerRoster(ManagersSample.map {
        if (it.id == "m-8b04d2") it.copy(lastSeen = Instant.now().minusSeconds(3 * 3600).toString()) else it
    }, "assistant")

    private val api = object : ExtensionApiStub() {
        override suspend fun listing() = ExtensionListing(listOf(
            HubExtension("example-notes", "示例笔记", "connectors", "running", source = "installed", enabled = true),
            HubExtension("example-voice", "示例语音", "voice", "disabled", source = "builtin", manager = "assistant"),
            HubExtension("example-mail", "工作邮箱", "connectors", "running", source = "installed", enabled = true, manager = "m-3fa21c"),
            HubExtension("example-nas", "家里的相册", "connectors", "running", source = "installed", enabled = true, manager = "m-8b04d2"),
        ), canInstall = true)
    }

    private fun capture(name: String) {
        rule.waitForIdle()
        val image = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
        assertTrue(image.width > 0 && image.height > 0)
        val dir = File(System.getProperty("persona.shots") ?: "build/managers-shots").also { it.mkdirs() }
        File(dir, "$name.png").outputStream().use { image.compress(Bitmap.CompressFormat.PNG, 100, it) }
    }

    private fun shown(text: String) = rule.onAllNodesWithText(text).fetchSemanticsNodes().isNotEmpty()

    @Test fun extensionsAreTheCurrentAssistants() {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        AssistantManagerChoice.choose("hub", null, "assistant")
        rule.setContent {
            PersonaTheme(style = style, darkTheme = dark, reduceMotion = true) {
                CompositionLocalProvider(LocalManagerRoster provides roster) {
                    Box(Modifier.testTag("shot").fillMaxSize().background(PersonaTheme.palette.background)) { ExtensionsPage(api, {}, { _, _ -> }) }
                }
            }
        }
        rule.waitUntil { shown("示例笔记") }
        // The default's: its own and the unstamped one, under the list's one heading; no assistant headings.
        assertTrue(shown("示例语音") && shown("这台 hub 的扩展"))
        assertFalse(shown("工作邮箱") || shown("家里的相册") || shown("助手") || shown("工作") || shown("家里"))
        for (s in AppStyle.entries) for (d in listOf(false, true)) {
            style = s; dark = d
            capture("plugins-current-${s.key}-${if (d) "dark" else "light"}")
        }
        // Another assistant current: that one's alone.
        rule.runOnIdle { AssistantManagerChoice.pick("m-3fa21c") }
        rule.waitUntil { shown("工作邮箱") }
        assertFalse(shown("示例笔记") || shown("示例语音") || shown("家里的相册"))
        style = AppStyle.Glass; dark = false
        capture("plugins-current-work-glass-light")
    }

    /** The merged list as the hub sends it with several managers: a built-in once per manager. */
    private val twice = object : ExtensionApiStub() {
        override suspend fun listing() = ExtensionListing(listOf(
            HubExtension("example-voice", "示例语音", "voice", "disabled", source = "builtin", manager = "assistant"),
            HubExtension("example-voice", "示例语音", "voice", "disabled", source = "builtin", manager = "m-3fa21c"),
        ), canInstall = true)
    }

    /** Before the roster is read the current assistant is the one kept on the device: its copy of a built-in alone. */
    @Test fun beforeTheRosterTheKeptAssistantsCopy() {
        AssistantManagerChoice.choose("hub", "m-3fa21c", "assistant")
        val opened = mutableListOf<Pair<String, String?>>()
        rule.setContent {
            PersonaTheme(reduceMotion = true) {
                CompositionLocalProvider(LocalManagerRoster provides null) {
                    Box(Modifier.fillMaxSize()) { ExtensionsPage(twice, {}, { id, manager -> opened += id to manager }) }
                }
            }
        }
        rule.waitUntil { rule.onAllNodesWithText("示例语音").fetchSemanticsNodes().size == 1 }
        rule.onNodeWithText("示例语音").performClick()
        assertEquals(listOf<Pair<String, String?>>("example-voice" to "m-3fa21c"), opened)
    }

    /** A built-in is listed under every manager: the current assistant's copy opens its own, and is the selected one. */
    @Test fun aBuiltInIsTheCurrentAssistantsOwn() {
        AssistantManagerChoice.choose("hub", "m-3fa21c", "assistant")
        val opened = mutableListOf<Pair<String, String?>>()
        rule.setContent {
            PersonaTheme(reduceMotion = true) {
                CompositionLocalProvider(LocalManagerRoster provides roster, LocalDetailKey provides ExtensionRoute("example-voice", manager = "m-3fa21c")) {
                    Box(Modifier.fillMaxSize()) { ExtensionsPage(twice, {}, { id, manager -> opened += id to manager }) }
                }
            }
        }
        rule.waitUntil { rule.onAllNodesWithText("示例语音").fetchSemanticsNodes().size == 1 }
        assertEquals(1, rule.onAllNodes(hasText("示例语音") and isSelected()).fetchSemanticsNodes().size)
        rule.onNodeWithText("示例语音").performClick()
        // The default current: its own copy, opened as the default's (no manager named).
        rule.runOnIdle { AssistantManagerChoice.pick(null) }
        rule.waitForIdle()
        assertEquals(0, rule.onAllNodes(hasText("示例语音") and isSelected()).fetchSemanticsNodes().size)
        rule.onNodeWithText("示例语音").performClick()
        assertEquals(listOf<Pair<String, String?>>("example-voice" to "m-3fa21c", "example-voice" to null), opened)
    }
}
