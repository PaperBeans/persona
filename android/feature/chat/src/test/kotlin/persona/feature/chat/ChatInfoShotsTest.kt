package persona.feature.chat

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.remember
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.isDialog
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.MutableStateFlow
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.Interaction
import persona.core.contracts.InteractionRecord
import persona.core.contracts.Item
import persona.core.contracts.Machine
import persona.core.contracts.Question
import persona.core.contracts.QuestionOption
import persona.core.contracts.Resolution
import persona.core.contracts.Session
import persona.core.contracts.SessionInfo
import persona.core.contracts.SessionKey
import persona.core.contracts.api.ChatAction
import persona.core.contracts.api.ChatActionResult
import persona.core.contracts.api.ChatActions
import persona.core.contracts.api.ChatRepository
import persona.core.contracts.api.ChatSnapshot
import persona.core.contracts.api.DeliveryState
import persona.core.contracts.api.InteractionCenter
import persona.core.contracts.api.MessageSender
import persona.core.contracts.api.OutboxMessage
import persona.core.contracts.api.SessionsRepository
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaTheme
import java.io.File
import java.time.Instant

/**
 * #4339 C9, second batch, on this branch only (they use what it adds): Session info with the Lark link, the questions the
 * assistant answered, the Codex route and the context left (every style, on a tall screen), and on Glass a
 * message that did not go kept where it was written, before the message sent after it. PNGs in
 * `build/chat-details-more-shots/` (or `-Dpersona.shots=<dir>`). Native JVM rendering: no system bars, no glass refraction.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h760dp-xhdpi")
class ChatInfoShotsTest {
    @get:Rule val rule = createComposeRule()

    private val key = SessionKey("computer-a", "importer")
    private val start = Instant.parse("2026-11-12T01:00:00Z")
    private fun at(minutes: Long) = start.plusSeconds(minutes * 60)
    private val machine = Machine(key.machineId, "Computer A", online = true)
    private val codex = Session(key.machineId, key.sessionId, title = "Fix the importer retry", agent = "codex", status = "idle",
        cwd = "/home/me/code/app", spaceName = "app", model = "gpt-5.5", launcher = "codex", externalUrl = "https://example.com/topic/42",
        codexRoute = "terminal", codexRouteNote = "Its app-server isn't running on this computer, so Persona reads the terminal.",
        contextTokens = 78_000, contextWindow = 200_000)

    private fun answered(id: String, question: String, options: List<String>, answer: String, reason: String, minutesAgo: Long) =
        InteractionRecord(Interaction(id, key.machineId, key.sessionId, kind = "question",
            questions = listOf(Question(question, options = options.map { QuestionOption(it) }))),
            "resolved", Resolution(answers = mapOf(question to answer), answeredBy = "assistant", reason = reason),
            Instant.now().minusSeconds(minutesAgo * 60).toString())

    private val actions = object : ChatActions {
        override suspend fun isMuted(session: Session) = false
        override suspend fun execute(session: Session, action: ChatAction) = ChatActionResult.Done
        override suspend fun verify(session: Session, action: ChatAction) = ChatActionResult.Done
        override suspend fun info(session: Session) = SessionInfo(sessionId = key.sessionId, cwd = "/home/me/code/app", model = "gpt-5.5",
            version = "0.153.0", effort = "high", contextTokens = 78_000, outputTokens = 12_800, turns = 6, gitBranch = "fix/importer-retry",
            startedAt = "2026-11-12T01:00:00Z")
        override suspend fun interactionHistory(session: Session) = listOf(
            answered("q1", "Retry once or keep retrying?", listOf("Once", "Keep retrying"), "Once",
                "A second failure means the hub is down; retrying more only delays the error.", 12),
            answered("q2", "Add the test to the importer suite?", listOf("Yes", "No"), "Yes", "The suite runs on every change.", 95),
        )
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
        repeat(40) { rule.mainClock.advanceTimeByFrame() }
    }

    private fun save(name: String, style: AppStyle, dark: Boolean, image: Bitmap) {
        val output = File(System.getProperty("persona.shots") ?: "build/chat-details-more-shots").also { it.mkdirs() }
        File(output, "$name-${style.name.lowercase()}-${if (dark) "dark" else "light"}.png").outputStream()
            .use { assertTrue(image.compress(Bitmap.CompressFormat.PNG, 100, it)) }
    }

    private fun info(style: AppStyle, dark: Boolean) {
        rule.mainClock.autoAdvance = false
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                val resources = LocalResources.current
                val scope = rememberCoroutineScope()
                val chrome = remember { ChatChromeState(resources, actions, scope, {}, {}) }
                ChatSessionInfoSheet(codex, machine, chrome, onDismiss = {})
            }
        }
        waitFor("the answered questions") { "Retry once or keep retrying?" in it && "Model" in it }
        save("info", style, dark, rule.onNode(isDialog()).captureToImage().asAndroidBitmap())
    }

    // A tall screen, so the whole sheet shows in one picture.
    @Config(qualifiers = "en-w400dp-h1700dp-xhdpi") @Test fun infoGlassLight() = info(AppStyle.Glass, false)
    @Config(qualifiers = "en-w400dp-h1700dp-xhdpi") @Test fun infoGlassDark() = info(AppStyle.Glass, true)
    @Config(qualifiers = "en-w400dp-h1700dp-xhdpi") @Test fun infoBriefLight() = info(AppStyle.Brief, false)
    @Config(qualifiers = "en-w400dp-h1700dp-xhdpi") @Test fun infoBoardDark() = info(AppStyle.Board, true)

    /** On Glass a message that did not go stays where it was written: before the message sent after it. */
    private fun placed(dark: Boolean) {
        val items = listOf(
            Item("u1", 1, role = "user", kind = "text", text = "Make the importer retry once when the hub restarts", ts = at(0).toString()),
            Item("a2", 2, role = "assistant", kind = "text", text = "Done. The importer now retries once after a restart.", ts = at(1).toString()),
            Item("u3", 3, role = "user", kind = "text", text = "Run the importer tests again", ts = at(10).toString()),
            Item("a4", 4, role = "assistant", kind = "text", text = "All 42 importer tests pass.", ts = at(11).toString()),
        )
        val outbox = listOf(
            OutboxMessage("m1", key, "Also add a line to the release notes", DeliveryState.Rejected, createdAt = at(3).toEpochMilli()),
            OutboxMessage("m2", key, "And bump the version", DeliveryState.WaitingForNetwork, createdAt = at(12).toEpochMilli()),
        )
        val sessions = object : SessionsRepository {
            override val machines = MutableStateFlow(listOf(machine))
            override val sessions = MutableStateFlow(listOf(codex.copy(agent = "claude", codexRoute = null, model = null, launcher = null)))
            override suspend fun refresh() = Unit
        }
        val chats = object : ChatRepository {
            override fun observe(session: SessionKey) = MutableStateFlow(ChatSnapshot(items = items, loading = false))
            override suspend fun loadOlder(session: SessionKey) = Unit
            override suspend fun expandFold(session: SessionKey, fromSeq: Int, toSeq: Int): List<Item> = emptyList()
        }
        val sender = object : MessageSender {
            override val outbox = MutableStateFlow(outbox)
            override suspend fun send(session: SessionKey, text: String, clientMessageId: String, clearDraft: String?) = Unit
        }
        val interactions = object : InteractionCenter {
            override val pending = MutableStateFlow(emptyList<Interaction>())
            override suspend fun refresh() = Unit
            override suspend fun resolve(id: String, resolution: Resolution) = Unit
        }
        rule.mainClock.autoAdvance = false
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = AppStyle.Glass) {
                Box(Modifier.fillMaxSize().background(PersonaTheme.palette.background).testTag("shot")) {
                    ChatScreen(key, chats, sender, interactions, sessions, onBack = {})
                }
            }
        }
        waitFor("the chat") { "And bump the version" in it && "All 42 importer tests pass." in it }
        save("unsent-placed", AppStyle.Glass, dark, rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap())
    }

    @Test fun placedGlassLight() = placed(false)
    @Test fun placedGlassDark() = placed(true)
}
