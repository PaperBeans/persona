package persona.feature.chat

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextReplacement
import androidx.test.ext.junit.runners.AndroidJUnit4
import java.io.File
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.FeedItem
import persona.core.contracts.FeedResponse
import persona.core.contracts.ServerEvent
import persona.core.contracts.TaskPlan
import persona.core.contracts.Todo
import persona.core.contracts.api.ConnectionState
import persona.core.contracts.api.HubConnection
import persona.core.contracts.api.MessageDraftDismissal
import persona.core.contracts.api.TasksApi
import persona.core.designsystem.AppFeedback
import persona.core.designsystem.AppStyle
import persona.core.designsystem.FeedbackAction
import persona.core.designsystem.LocalAppFeedback
import persona.core.designsystem.PersonaTheme
import persona.core.domain.tasks.TasksStore
import androidx.compose.runtime.setValue

/** Native JVM snapshots of the real feed card and the standard application snackbar; not emulator captures. */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "zh-w400dp-h1200dp-xhdpi")
class MessageDraftCardTest {
    @get:Rule val rule = createComposeRule()
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Unconfined)
    @After fun close() = scope.cancel()

    private val original = FeedItem("draft-one", "conversation", "2026-11-11T09:00:00Z", "message", "source-one", buildJsonObject {
        put("sender", "Example sender"); put("chat", "Release discussion")
        put("text", "How is the task going?"); put("draft", "The fix is ready for review.")
    })
    private class Api(var item: FeedItem) : TasksApi {
        val dismissals = mutableListOf<Pair<String, String>>()
        val undos = mutableListOf<Pair<String, String>>()
        override suspend fun todos() = emptyList<Todo>()
        override suspend fun feed(conversation: String, since: String?) = FeedResponse(listOf(item))
        override suspend fun startPlan(id: String, remember: Boolean): TaskPlan = error("unused")
        override suspend fun dismissPlan(id: String): TaskPlan = error("unused")
        override suspend fun retryTask(todoId: String): Todo = error("unused")
        override suspend fun removeTask(todoId: String): Unit = error("dismissal must not remove a task")
        override suspend fun route(conversation: String, todoId: String, text: String): FeedItem = error("unused")
        override suspend fun today(conversation: String): FeedItem = error("unused")
        override suspend fun mainConversation(): String? = null
        override suspend fun inboxReply(itemId: String, text: String, card: String?): Unit = error("dismissal must not send")
        override suspend fun dismissMessage(cardId: String, draft: String): MessageDraftDismissal {
            dismissals += cardId to draft
            item = item.copy(body = JsonObject((item.body as JsonObject) + mapOf(
                "dismissed_at" to JsonPrimitive("2026-11-11T10:00:00Z"), "dismissal_id" to JsonPrimitive("dismissal-one"))))
            return MessageDraftDismissal(item, "dismissal-one")
        }
        override suspend fun undoDismissMessage(cardId: String, dismissalId: String): FeedItem {
            undos += cardId to dismissalId
            item = item.copy(body = JsonObject((item.body as JsonObject) - setOf("dismissed_at", "dismissal_id")))
            return item
        }
    }
    private class Connection : HubConnection {
        override val state = MutableStateFlow(ConnectionState.WaitingForNetwork)
        override val events = MutableSharedFlow<ServerEvent>()
        override fun reconnect() = Unit
        override fun send(message: JsonObject) = true
    }
    private class Feedback : AppFeedback {
        var action: FeedbackAction? = null
        var message: String? = null
        val pending = MutableStateFlow<persona.core.designsystem.FeedbackNotice?>(null)
        override fun failure(error: Throwable, operation: Boolean): Boolean = false
        override fun notice(message: String) = false
        override fun notice(message: String, action: FeedbackAction): Boolean {
            this.message = message; this.action = action
            pending.value = persona.core.designsystem.FeedbackNotice(message, action)
            return true
        }
    }

    private fun scenario(language: String, dismiss: String, undo: String, notice: String, textScale: Float = 1f) {
        val api = Api(original)
        val store = TasksStore(api, Connection(), scope)
        store.load(original.conversation)
        val feedback = Feedback()
        val appearance = mutableStateOf(AppStyle.Glass to false)
        rule.setContent {
            val density = androidx.compose.ui.platform.LocalDensity.current
            val configuration = android.content.res.Configuration(androidx.compose.ui.platform.LocalConfiguration.current).apply {
                fontScale = textScale
                uiMode = (uiMode and android.content.res.Configuration.UI_MODE_NIGHT_MASK.inv()) or
                    if (appearance.value.second) android.content.res.Configuration.UI_MODE_NIGHT_YES else android.content.res.Configuration.UI_MODE_NIGHT_NO
            }
            CompositionLocalProvider(LocalAppFeedback provides feedback,
                androidx.compose.ui.platform.LocalConfiguration provides configuration,
                androidx.compose.ui.platform.LocalDensity provides androidx.compose.ui.unit.Density(density.density, textScale)) {
                PersonaTheme(style = appearance.value.first, darkTheme = appearance.value.second, reduceMotion = true) {
                    val feeds by store.feeds.collectAsState()
                    val pending by feedback.pending.collectAsState()
                    // The app's toast, kept up for the image matrix (the host times it out); its Undo runs the action.
                    var shown by remember { mutableStateOf<persona.core.designsystem.FeedbackNotice?>(null) }
                    LaunchedEffect(pending) { pending?.let { shown = it } }
                    val host = AssistantTaskHost(store, original.conversation, emptyList(), { it }, {}, { throw it }, {})
                    Column(Modifier.fillMaxWidth().background(PersonaTheme.palette.background).testTag("draft-scene"),
                        verticalArrangement = Arrangement.spacedBy(persona.core.designsystem.Tokens.Space.s)) {
                        feeds[original.conversation].orEmpty().forEach {
                            Box(Modifier.testTag("draft-history-card")) {
                                AssistantExtraLine(persona.core.domain.tasks.AssistantExtra.Feed(it), host)
                            }
                        }
                        shown?.let { persona.core.designsystem.JToast(it, dismiss = { shown = null }, modifier = Modifier.testTag("draft-undo-snackbar")) }
                    }
                }
            }
        }
        val images = mutableListOf<ByteArray>()
        for (style in AppStyle.entries) for (dark in listOf(false, true)) {
            rule.runOnIdle { appearance.value = style to dark; Snapshot.sendApplyNotifications() }
            rule.mainClock.advanceTimeBy(500)
            rule.onNodeWithTag("assistant-draft-send-draft-one").assertIsDisplayed()
            rule.onNodeWithTag("assistant-draft-later-draft-one").assertIsDisplayed()
            rule.onNodeWithTag("assistant-draft-dismiss-draft-one").assertIsDisplayed().assertTextEquals(dismiss)
            val bitmap = rule.onNodeWithTag("draft-scene").captureToImage().asAndroidBitmap()
            val output = File("build/message-draft-component-shots/$language-${style.name.lowercase()}-${if (dark) "dark" else "light"}-font$textScale.png")
            output.parentFile.mkdirs()
            output.outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
            images += output.readBytes()
        }
        images.chunked(2).forEach { assertFalse("dark/light component images must differ", it[0].contentEquals(it[1])) }
        rule.onNode(hasSetTextAction()).performTextReplacement("An edited draft must not become the version token")
        rule.onNodeWithTag("assistant-draft-dismiss-draft-one").performClick()
        rule.mainClock.advanceTimeBy(500)
        rule.onNodeWithTag("assistant-draft-dismiss-draft-one").assertDoesNotExist()
        rule.onNodeWithTag("assistant-draft-send-draft-one").assertDoesNotExist()
        rule.onNodeWithTag("assistant-draft-later-draft-one").assertDoesNotExist()
        rule.onAllNodes(hasSetTextAction()).assertCountEquals(0)
        rule.onAllNodesWithText(notice).assertCountEquals(2)
        rule.onNodeWithText(undo).assertIsDisplayed()
        rule.onNodeWithText(original.field("draft")!!).assertIsDisplayed()
        for (style in AppStyle.entries) for (dark in listOf(false, true)) {
            rule.runOnIdle { appearance.value = style to dark; Snapshot.sendApplyNotifications() }
            rule.onAllNodesWithText(notice).assertCountEquals(2)
            rule.onNodeWithText(undo).assertIsDisplayed()
            val bitmap = rule.onNodeWithTag("draft-history-card").captureToImage().asAndroidBitmap()
            val prefix = "build/message-draft-component-shots/$language-${style.name.lowercase()}-${if (dark) "dark" else "light"}-font$textScale"
            File("$prefix-history.png").outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
            val withUndo = rule.onNodeWithTag("draft-scene").captureToImage().asAndroidBitmap()
            File("$prefix-undo-snackbar.png").outputStream().use { withUndo.compress(Bitmap.CompressFormat.PNG, 100, it) }
        }
        assertEquals(listOf(original.id to original.field("draft")), api.dismissals)
        assertEquals(notice, feedback.message)
        assertEquals(undo, feedback.action?.label)
        rule.onNodeWithText(undo).performClick()
        rule.mainClock.advanceTimeBy(500)
        rule.onNodeWithTag("assistant-draft-dismiss-draft-one").assertIsDisplayed()
        assertEquals(listOf(original.id to "dismissal-one"), api.undos)
        assertTrue(store.feeds.value[original.conversation]!!.single().canSend)
    }

    @Test @Config(qualifiers = "en-w400dp-h1200dp-xhdpi")
    fun englishLargeText() = scenario("en", "Don't handle", "Undo", "Draft dismissed; nothing was sent", textScale = 2f)
    @Test fun chineseLargeText() = scenario("zh", "不处理", "撤销", "已选择不处理，草稿没有发送", textScale = 2f)
    @Test @Config(qualifiers = "ja-w400dp-h1200dp-xhdpi")
    fun japaneseLargeText() = scenario("ja", "対応しない", "元に戻す", "対応を見送りました。下書きは送信していません。", textScale = 2f)

    @Test fun chinese() = scenario("zh", "不处理", "撤销", "已选择不处理，草稿没有发送")
    @Test @Config(qualifiers = "en-w400dp-h1200dp-xhdpi")
    fun english() = scenario("en", "Don't handle", "Undo", "Draft dismissed; nothing was sent")
    @Test @Config(qualifiers = "ja-w400dp-h1200dp-xhdpi")
    fun japanese() = scenario("ja", "対応しない", "元に戻す", "対応を見送りました。下書きは送信していません。")
}
