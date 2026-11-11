package persona.feature.machines

import android.graphics.Bitmap
import androidx.activity.ComponentActivity
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextInput
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.flowOf
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.Machine
import persona.core.contracts.api.AssistantManagerChoice
import persona.core.contracts.api.ComputerCode
import persona.core.contracts.api.ComputerOs
import persona.core.contracts.api.ComputerPairing
import persona.core.contracts.api.HubManager
import persona.core.contracts.api.ManagerRoster
import persona.core.designsystem.AppStyle
import persona.core.designsystem.LocalManagerRoster
import persona.core.designsystem.PersonaTheme
import java.io.File

/**
 * Adding a computer with several assistants (docs/assistant/managers.md "Clients"): the waiting page says when the hub put
 * the computer under another assistant than the current one, and an offline current assistant gets none (Next off, and
 * why). Glass light and dark in Chinese, light in English and Japanese, with the mock hub's names, into
 * `build/onboarding-shots/add-computer-{under,offline}-*.png`.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class AddComputerAssistantShotsTest {
    @get:Rule val rule = createAndroidComposeRule<ComponentActivity>()
    @After fun reset() = AssistantManagerChoice.reset()

    private fun out(): File = File(System.getProperty("persona.shots") ?: "build/onboarding-shots").also { it.mkdirs() }

    private val roster = ManagerRoster(listOf(
        HubManager("assistant", "助手", online = true, isDefault = true, builtin = true, host = "server"),
        HubManager("m-3fa21c", "工作", online = true, host = "MacBook"),
        HubManager("m-8b04d2", "家里", online = false, lastSeen = "2026-11-05T03:00:00Z"),
    ), "assistant")
    private val pair = "persona://link?url=https%3A%2F%2Fhub.example.com&token=jp_example&machine=macbook&hub=example&fp=example&tls=18662"
    private val asked = mutableListOf<String?>()
    private var full = false
    private class Full : Exception("refused"), persona.core.contracts.api.HubCodedFailure {
        override val status = 429
        override val code = "too_many_pending"
    }
    private val api = object : ComputerPairing {
        override val canAdd = flowOf(true)
        override suspend fun add(name: String, os: ComputerOs) = add(name, os, null)
        // A hub from before add.manager: no manager in the answer, the computer under its own assistant.
        override suspend fun add(name: String, os: ComputerOs, manager: String?): ComputerCode {
            asked += manager
            if (full) throw Full()
            return ComputerCode("macbook", name, pair, System.currentTimeMillis() + 598_000, command = "persona-link pair '$pair'")
        }
        override suspend fun again(machineId: String) = add(machineId, ComputerOs.Mac)
        override suspend fun machines(machineId: String) = emptyList<Machine>()
        override suspend fun confirm(machineId: String) {}
        override suspend fun reject(machineId: String) {}
    }

    private fun page(dark: Boolean, current: String, listed: List<Machine> = emptyList()) {
        AssistantManagerChoice.choose("hub", current, "assistant")
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = AppStyle.Glass) {
                CompositionLocalProvider(LocalManagerRoster provides roster) {
                    Box(Modifier.testTag("shot").width(400.dp).height(1100.dp).background(PersonaTheme.palette.background)) {
                        AddComputerPage(api, MutableStateFlow(listed), null, onBack = {})
                    }
                }
            }
        }
        rule.waitForIdle()
    }

    private fun capture(name: String) {
        rule.waitForIdle()
        val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
        File(out(), "add-computer-$name.png").outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
    }

    private fun under(language: String, dark: Boolean, next: String, said: String) {
        page(dark, "m-3fa21c")
        rule.onNode(hasSetTextAction()).performTextInput("MacBook")
        rule.onNodeWithText(next).performClick()
        rule.waitForIdle()
        assertEquals(listOf<String?>("m-3fa21c"), asked)
        rule.onNodeWithText(said).assertExists()
        capture("under-$language-${if (dark) "dark" else "light"}")
    }

    private fun offline(language: String, dark: Boolean, next: String, said: String) {
        page(dark, "m-8b04d2")
        rule.onNodeWithText(said).assertExists()
        // Nothing to fill in or send: only Close.
        assertTrue(rule.onAllNodes(hasSetTextAction()).fetchSemanticsNodes().isEmpty())
        assertTrue(rule.onAllNodesWithText(next).fetchSemanticsNodes().isEmpty())
        capture("offline-$language-${if (dark) "dark" else "light"}")
        assertTrue(asked.isEmpty())
    }

    /** Three computers wait (the hub's limit): every assistant's are listed, each with Remove, not only the current one's. */
    @Config(sdk = [35], qualifiers = "zh-w400dp-h1100dp-xhdpi")
    @Test fun inTheWayZhLight() {
        full = true
        page(false, "m-3fa21c", listOf(
            Machine("a", "server", trust = "pending", errorCode = "never_connected"),
            Machine("b", "MacBook", trust = "pending", online = true, manager = "m-3fa21c"),
            Machine("c", "NAS", trust = "pending", manager = "m-8b04d2"),
        ))
        rule.onNode(hasSetTextAction()).performTextInput("Desk")
        rule.onNodeWithText("下一步").performClick()
        rule.waitForIdle()
        listOf("server", "MacBook", "NAS", "还没连上来过", "等你确认", "离线").forEach { rule.onNodeWithText(it).assertExists() }
        assertEquals(3, rule.onAllNodesWithText("删掉").fetchSemanticsNodes().size)
        capture("in-the-way-zh-light")
    }

    @Config(sdk = [35], qualifiers = "zh-w400dp-h1100dp-xhdpi")
    @Test fun underZhLight() = under("zh", false, "下一步", "这台电脑加到了「助手」名下。")

    @Config(sdk = [35], qualifiers = "zh-w400dp-h1100dp-night-xhdpi")
    @Test fun underZhDark() = under("zh", true, "下一步", "这台电脑加到了「助手」名下。")

    @Config(sdk = [35], qualifiers = "en-w400dp-h1100dp-xhdpi")
    @Test fun underEn() = under("en", false, "Next", "This computer was added under 助手.")

    @Config(sdk = [35], qualifiers = "ja-w400dp-h1100dp-xhdpi")
    @Test fun underJa() = under("ja", false, "次へ", "このコンピューターは「助手」に追加されました。")

    @Config(sdk = [35], qualifiers = "zh-w400dp-h1100dp-xhdpi")
    @Test fun offlineZhLight() = offline("zh", false, "下一步", "「家里」离线，暂时不能给它添加电脑。")

    @Config(sdk = [35], qualifiers = "zh-w400dp-h1100dp-night-xhdpi")
    @Test fun offlineZhDark() = offline("zh", true, "下一步", "「家里」离线，暂时不能给它添加电脑。")

    @Config(sdk = [35], qualifiers = "en-w400dp-h1100dp-xhdpi")
    @Test fun offlineEn() = offline("en", false, "Next", "家里 is offline, so a computer can't be added to it right now.")

    @Config(sdk = [35], qualifiers = "ja-w400dp-h1100dp-xhdpi")
    @Test fun offlineJa() = offline("ja", false, "次へ", "「家里」はオフラインのため、今はコンピューターを追加できません。")
}
