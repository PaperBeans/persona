package persona.feature.assistant

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.isDialog
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.SessionKey
import persona.core.contracts.api.*
import persona.core.designsystem.*
import persona.feature.chat.ChatComposer
import persona.feature.chat.rememberChatComposerState
import java.io.File

/**
 * New chat's composer on Glass (#5055): the model pill in place of the launcher chip, and its panel with "For this new
 * chat · …"; a pick is what the chat starts on. Main draws the launcher chip in the same scene. PNGs `model-newchat-*`
 * in `build/model-pill-shots/` (or `-Dpersona.shots=<dir>`).
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w720dp-h760dp-xhdpi")
class NewChatModelPillShotsTest {
    @get:Rule val rule = createComposeRule()

    private val lists = mapOf(
        "claude" to LauncherModels("claude", listOf(AgentModel("default", "Default", resolved = "claude-opus-5-5", isDefault = true),
            AgentModel("opus", "Opus 5.5", resolved = "claude-opus-5-5", efforts = listOf("low", "medium", "high", "xhigh", "max"), defaultEffort = "high"),
            AgentModel("sonnet", "Sonnet 4.5"))),
        "team" to LauncherModels("claude", listOf(AgentModel("sonnet", "Sonnet 4.5"), AgentModel("haiku", "Haiku 4.5"))),
    )
    private val management = object : AssistantManagement {
        override suspend fun config() = AssistantConfig(machineId = "box", launcher = "claude", main = "main")
        override suspend fun assistantLaunchers(machineId: String) = listOf(AgentLauncher("claude"), AgentLauncher("team"))
        override suspend fun assistantLauncherList(machineId: String) = AssistantLaunchers( // branch-only
            listOf(AgentLauncher("claude"), AgentLauncher("team", displayName = "Team API"), AgentLauncher("cx", agentType = "codex")), // branch-only
            LauncherDefault("claude", "opus", "high")) // branch-only
        override suspend fun launcherModels(machineId: String, launcher: String) = lists[launcher]
        override suspend fun warm() = Unit
        override suspend fun endConversation(session: SessionKey) = Unit
        override suspend fun tasks() = emptyList<ScheduledTask>()
        override suspend fun saveTask(task: ScheduledTask) = task
        override suspend fun deleteTask(id: String) = Unit
        override suspend fun runTask(id: String) = SessionKey("box", "main")
        override suspend fun freshTask(id: String) = ScheduledTask(id)
        override suspend fun runs() = emptyList<Run>()
        override suspend fun notes() = AssistantNotes(machineId = "box")
        override suspend fun setNotes(text: String) = Unit
        override suspend fun memory() = emptyList<MemoryFile>()
        override suspend fun saveMemory(name: String, content: String) = Unit
        override suspend fun deleteMemory(name: String) = Unit
    }

    private fun shot(dark: Boolean) {
        var pick by mutableStateOf<persona.core.contracts.ModelPick?>(null)
        rule.setContent {
            CompositionLocalProvider(LocalHubFeatures provides setOf("assistant_model_pick")) {
                PersonaTheme(darkTheme = dark, reduceMotion = true, style = AppStyle.Glass) {
                    Column(Modifier.testTag("shot").fillMaxWidth().height(360.dp).background(PersonaTheme.palette.background)) {
                        Spacer(Modifier.weight(1f))
                        ChatComposer(SessionKey("box", "pending"), rememberChatComposerState(), working = false, sending = false,
                            canClear = false, service = null, onSend = {}, onCommand = {}, onNotice = {}, placeholder = "Ask Persona…",
                            //main: accessory = { StarterLauncherChip(pick?.launcher, "claude", listOf(AgentLauncher("claude"), AgentLauncher("team")), failed = false, enabled = true) { pick = it?.let { l -> persona.core.contracts.ModelPick(l) } } })
                            modelPill = { width -> persona.feature.chat.PersonaModelPill(null, "box", management, width, pick, { pick = it }, {}) }) // branch-only
                    }
                }
            }
        }
        rule.onNode(hasSetTextAction()).performClick()
        rule.waitForIdle()
        rule.waitUntil(10_000) { rule.onAllNodes(hasText("Default: claude", substring = true) or hasText("Opus", substring = true), useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty() || //main-or
            rule.onAllNodes(androidx.compose.ui.test.hasContentDescription("Select model", substring = true)).fetchSemanticsNodes().isNotEmpty() }
        rule.waitForIdle()
        save("newchat", dark, rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap())
        rule.onNodeWithTag("model-pill").performClick() // branch-only
        rule.waitUntil(10_000) { rule.onAllNodes(hasText("For this new chat", substring = true), useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty() } // branch-only
        rule.waitForIdle() // branch-only
        save("newchat-panel", dark, rule.onNode(isDialog()).captureToImage().asAndroidBitmap()) // branch-only
        // the providers that can run Persona chats there, the default's model running: Recommended // branch-only
        rule.onAllNodes(hasText("Team API") and androidx.compose.ui.test.hasAnyAncestor(isDialog()), useUnmergedTree = true) // branch-only
            .fetchSemanticsNodes().let { assertTrue("Team API is a group", it.isNotEmpty()) } // branch-only
    }

    private fun save(name: String, dark: Boolean, image: Bitmap) {
        val output = File(System.getProperty("persona.shots") ?: "build/model-pill-shots").also { it.mkdirs() }
        File(output, "model-$name-glass-${if (dark) "dark" else "light"}.png").outputStream()
            .use { assertTrue(image.compress(Bitmap.CompressFormat.PNG, 100, it)) }
    }

    @Test fun light() = shot(false)
    @Test fun dark() = shot(true)
}
