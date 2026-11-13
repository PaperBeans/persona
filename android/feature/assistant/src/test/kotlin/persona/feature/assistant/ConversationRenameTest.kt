package persona.feature.assistant

import android.graphics.Bitmap
import androidx.activity.ComponentActivity
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.Text
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.test.assertIsEnabled
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasScrollToNodeAction
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.isDialog
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.longClick
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollToNode
import androidx.compose.ui.test.performTextReplacement
import androidx.compose.ui.test.performTouchInput
import androidx.compose.ui.test.assertCountEquals
import androidx.navigation3.runtime.NavKey
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.Session
import persona.core.designsystem.AppFeedback
import persona.core.designsystem.AppStyle
import persona.core.designsystem.FeedbackStyle
import persona.core.designsystem.LocalAppFeedback
import persona.core.designsystem.PersonaTheme
import persona.core.navigation.AppTab
import persona.core.navigation.Navigator
import java.io.File

private val main = Session(machineId = "hub", sessionId = "main", title = "主对话", status = "idle")
private val first = Session(machineId = "hub", sessionId = "a", title = "第一个任务", status = "idle", updatedAt = "2026-11-12T09:30:00Z")
private val second = Session(machineId = "hub", sessionId = "b", title = "第二个任务", status = "working")

/**
 * C12 (#4339): Rename… on a history row (iOS `RenameSessionAlert`) sends the title to the hub's rename route and the row
 * shows it. At Robolectric's own screen size: under any other, a Compose dialog holding a text field never gets idle.
 */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "zh-xhdpi")
class ConversationRenameTest {
    @get:Rule val rule = createAndroidComposeRule<ComponentActivity>()
    private val notices = mutableListOf<Pair<String, FeedbackStyle>>()
    private val navigator = object : Navigator {
        override fun navigate(key: NavKey) = Unit
        override fun back() = Unit
        override fun selectTab(tab: AppTab) = Unit
    }

    private fun show(hub: HistoryHub): HistoryHub {
        rule.setContent {
            CompositionLocalProvider(LocalAppFeedback provides object : AppFeedback {
                override fun failure(failure: Throwable, operation: Boolean): Boolean {
                    notices += failure.message.orEmpty() to FeedbackStyle.Error
                    return true
                }
                override fun notice(message: String) = false
                override fun notice(message: String, style: FeedbackStyle): Boolean {
                    notices += message to style
                    return true
                }
            }) {
                AssistantScreen(navigator, hub, hub, startConversation = { error("Unexpected creation") },
                    requestedSession = main.key, management = hub, composerService = null,
                    chatContent = { Text("chat:${it.sessionId}") })
            }
        }
        rule.onNodeWithContentDescription("对话列表").performClick()
        return hub
    }

    private fun rename(to: String) {
        rule.onNode(hasScrollToNodeAction()).performScrollToNode(hasText(first.title))
        rule.onNode(hasText(first.title) and hasClickAction()).performTouchInput { longClick() }
        rule.onNodeWithText("重命名…").performClick()
        rule.onNode(hasText("保存") and hasClickAction()).assertIsNotEnabled()
        rule.onNode(hasSetTextAction() and hasAnyAncestor(hasTestTag("rename-conversation"))).performTextReplacement(to)
        rule.onNode(hasText("保存") and hasClickAction()).assertIsEnabled().performClick()
        rule.waitForIdle()
    }

    @Test fun renameSendsTheTitleAndTheRowShowsItUntilTheHubDoes() {
        val hub = show(HistoryHub(main, first, second))
        rename("  出差行程 ")
        assertEquals(listOf(first.key to "出差行程"), hub.renamed)
        rule.onNode(hasScrollToNodeAction()).performScrollToNode(hasText("出差行程"))
        rule.onAllNodesWithText(first.title).assertCountEquals(0)
        assertTrue(notices.toString(), "已重命名" to FeedbackStyle.Success in notices)
        // The hub's list catches up, then Persona renames it: the new name shows.
        rule.runOnIdle { hub.assistant.value = hub.assistant.value.copy(conversations = listOf(main, first.copy(title = "出差行程"), second)) }
        rule.waitForIdle()
        rule.runOnIdle { hub.assistant.value = hub.assistant.value.copy(conversations = listOf(main, first.copy(title = "1112 | 出行 | 周五出差"), second)) }
        rule.onNode(hasScrollToNodeAction()).performScrollToNode(hasText("1112 | 出行 | 周五出差"))
    }

    @Test fun aFailedRenameKeepsTheTitleAndSaysWhy() {
        show(HistoryHub(main, first, second).apply { refuseRename = true })
        rename("出差行程")
        rule.onNode(hasScrollToNodeAction()).performScrollToNode(hasText(first.title))
        assertTrue(notices.toString(), "改名失败" to FeedbackStyle.Error in notices)
    }
}

/**
 * C12's rename dialog drawn on the JVM (Robolectric native graphics), Glass, light and dark, to look at:
 * `build/conversation-history-shots/` (or `-Dpersona.shots=<dir>`).
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "zh-xhdpi")
class ConversationRenameShotsTest {
    @get:Rule val rule = createComposeRule()

    @Test fun drawsTheRenameDialog() {
        var dark by mutableStateOf(false)
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = AppStyle.Glass) {
                key(dark) {
                    Box(Modifier.fillMaxSize().background(PersonaTheme.palette.background))
                    RenameConversationDialog(first, onDismiss = {}, onSave = {})
                }
            }
        }
        for (d in listOf(false, true)) {
            dark = d
            rule.waitForIdle()
            rule.onNodeWithText("会话名").assertExists()
            historyShot(rule.onNode(isDialog()).captureToImage().asAndroidBitmap(), "rename-glass-${if (d) "dark" else "light"}")
        }
    }
}

/** Writes a C12 shot, after checking it drew something. */
internal fun historyShot(bitmap: Bitmap, name: String) {
    val background = bitmap.getPixel(1, 1)
    var other = 0
    for (y in 0 until bitmap.height step 7) for (x in 0 until bitmap.width step 7) if (bitmap.getPixel(x, y) != background) other++
    assertTrue("$name drew nothing", other > 100)
    val out = File(System.getProperty("persona.shots") ?: "build/conversation-history-shots").also { it.mkdirs() }
    File(out, "$name.png").outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
}
