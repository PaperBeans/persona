package persona.feature.chat

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.isDialog
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.serialization.json.add
import kotlinx.serialization.json.buildJsonArray
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.chatlist.ChatAnchorStore
import persona.core.chatlist.ChatReadingKey
import persona.core.chatlist.ChatReadingPosition
import persona.core.contracts.Interaction
import persona.core.contracts.Item
import persona.core.contracts.Machine
import persona.core.contracts.Resolution
import persona.core.contracts.Session
import persona.core.contracts.SessionKey
import persona.core.contracts.api.ChatRepository
import persona.core.contracts.api.ChatSnapshot
import persona.core.contracts.api.InteractionCenter
import persona.core.contracts.api.MessageSender
import persona.core.contracts.api.OutboxMessage
import persona.core.contracts.api.SessionsRepository
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaTheme
import java.io.File
import java.time.Instant

/**
 * The plan pill and the sheet it opens on Glass (docs/plans/android-ios-alignment/chats.md C9, iOS `PlanSheet`), drawn by
 * the real chat page, light and dark: the page with the pill, then the sheet. PNGs in `build/chat-details-shots/` (or
 * `-Dpersona.shots=<dir>`).
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h760dp-xhdpi")
class ChatPlanSheetShotsTest {
    @get:Rule val rule = createComposeRule()

    private val key = SessionKey("computer-a", "importer")
    private val start = Instant.now().minusSeconds(3600)
    private fun at(seq: Int) = start.plusSeconds(seq * 20L).toString()
    private fun todo(content: String, status: String) = buildJsonObject { put("content", content); put("status", status) }
    private val items = buildList {
        add(Item("u1", 1, role = "user", kind = "text", text = "Make the importer retry once when the hub restarts", ts = at(1)))
        add(Item("t2", 2, role = "assistant", kind = "tool_use", toolUseId = "t2", toolName = "TodoWrite", ts = at(2),
            toolInput = buildJsonObject { put("todos", buildJsonArray {
                add(todo("Read the importer", "completed")); add(todo("Retry once after a restart", "completed"))
                add(todo("Add a test for the restart path", "in_progress")); add(todo("Update the release notes", "pending"))
            }) }))
        for (seq in 3..24) {
            add(Item("t$seq", seq, role = "assistant", kind = "tool_use", toolUseId = "t$seq", toolName = "Bash", ts = at(seq),
                toolInput = buildJsonObject { put("command", "./gradlew :importer:test --tests RestartRetry$seq") }))
            add(Item("t$seq-r", seq, role = "user", kind = "tool_result", toolUseId = "t$seq", text = "BUILD SUCCESSFUL", ts = at(seq)))
        }
        add(Item("a25", 25, role = "assistant", kind = "text", text = "The test passes; the release notes are next.", ts = at(25)))
    }

    private fun texts(): List<String> = rule.onAllNodes(SemanticsMatcher("any node") { true }, useUnmergedTree = true).fetchSemanticsNodes().flatMap { node ->
        node.config.getOrNull(SemanticsProperties.Text).orEmpty().map { it.text } +
            node.config.getOrNull(SemanticsProperties.ContentDescription).orEmpty()
    }

    private fun waitFor(what: String, condition: (List<String>) -> Boolean) {
        val deadline = System.currentTimeMillis() + 30_000
        while (true) {
            Snapshot.sendApplyNotifications()
            rule.mainClock.advanceTimeByFrame()
            if (condition(texts())) break
            check(System.currentTimeMillis() < deadline) { "$what never showed; shown: ${texts()}" }
            Thread.sleep(20)
        }
        repeat(30) { rule.mainClock.advanceTimeByFrame() }
    }

    private fun save(name: String, dark: Boolean, image: Bitmap) {
        val output = File(System.getProperty("persona.shots") ?: "build/chat-details-shots").also { it.mkdirs() }
        File(output, "$name-glass-${if (dark) "dark" else "light"}.png").outputStream()
            .use { assertTrue(image.compress(Bitmap.CompressFormat.PNG, 100, it)) }
    }

    private fun shot(dark: Boolean) {
        val sessions = object : SessionsRepository {
            override val machines = MutableStateFlow(listOf(Machine(key.machineId, "Computer A", online = true)))
            override val sessions = MutableStateFlow(listOf(Session(key.machineId, key.sessionId, title = "Fix the importer retry",
                agent = "claude", status = "idle")))
            override suspend fun refresh() = Unit
        }
        val chats = object : ChatRepository {
            override fun observe(session: SessionKey) = MutableStateFlow(ChatSnapshot(items = items, loading = false))
            override suspend fun loadOlder(session: SessionKey) = Unit
            override suspend fun expandFold(session: SessionKey, fromSeq: Int, toSeq: Int): List<Item> = emptyList()
        }
        val sender = object : MessageSender {
            override val outbox = MutableStateFlow(emptyList<OutboxMessage>())
            override suspend fun send(session: SessionKey, text: String, clientMessageId: String, clearDraft: String?) = Unit
        }
        val interactions = object : InteractionCenter {
            override val pending = MutableStateFlow(emptyList<Interaction>())
            override suspend fun refresh() = Unit
            override suspend fun resolve(id: String, resolution: Resolution) = Unit
        }
        // The turn's work line open, so its plan card is in the chat and scrolled out of view under the later steps.
        val anchors = object : ChatAnchorStore {
            override fun read(key: ChatReadingKey) = ChatReadingPosition(expanded = setOf("w-u1"))
            override fun write(key: ChatReadingKey, position: ChatReadingPosition) = Unit
        }
        rule.mainClock.autoAdvance = false
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = AppStyle.Glass) {
                Box(Modifier.fillMaxSize().background(PersonaTheme.palette.background).testTag("shot")) {
                    ChatScreen(key, chats, sender, interactions, sessions, onBack = {},
                        readingKey = ChatReadingKey("test", key.path), anchorStore = anchors)
                }
            }
        }
        waitFor("the plan pill") { "Step 3 of 4" in it }
        save("plan-pill", dark, rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap())
        rule.onNodeWithContentDescription("Step 3 of 4").performClick()
        waitFor("the plan sheet") { "Step 3 of 4 · Add a test for the restart path" in it }
        save("plan-sheet", dark, rule.onNode(isDialog()).captureToImage().asAndroidBitmap())
    }

    @Test fun light() = shot(false)
    @Test fun dark() = shot(true)
}
