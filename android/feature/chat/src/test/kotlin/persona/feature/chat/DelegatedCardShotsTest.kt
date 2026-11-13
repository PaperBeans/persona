package persona.feature.chat

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
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
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.MutableStateFlow
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.DelegatedAnswer
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
import persona.core.contracts.api.InteractionCenter
import persona.core.contracts.api.MessageSender
import persona.core.contracts.api.OutboxMessage
import persona.core.contracts.api.SessionsRepository
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaTheme
import java.io.File
import java.time.Instant

/**
 * #4339 C9: the latest question the assistant answered for an Agent, as iOS's `DelegatedCard` over the chat's composer
 * (Glass; Brief and Board draw none), the same card after the user changed an answer, the Change answer sheet with
 * another driver picked, and Session info's rows with Change answer. PNGs in `build/delegated-shots/` (or
 * `-Dpersona.shots=<dir>`). Native JVM rendering: no system bars, no glass refraction.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h900dp-xhdpi")
class DelegatedCardShotsTest {
    @get:Rule val rule = createComposeRule()

    private val key = SessionKey("computer-a", "importer")
    private val machine = Machine(key.machineId, "Computer A", online = true)
    private val row = Session(key.machineId, key.sessionId, title = "Move the importer to the new driver", status = "working", state = "working",
        agent = "claude", paneId = "p1", cwd = "/home/me/code/app", spaceName = "app", spaceId = "w1", slotName = "app")
    private val driver = "Which driver should the importer use?"
    private val tests = "Run the old importer tests first?"
    private val card = Interaction("ix_1", key.machineId, key.sessionId, kind = "question", questions = listOf(
        Question(driver, options = listOf(QuestionOption("pgx", "The rest of the code already uses it"), QuestionOption("sqlc", "Generates typed queries"))),
        Question(tests, options = listOf(QuestionOption("Yes"), QuestionOption("No"))),
    ))
    private val reason = "The rest of the code already uses pgx, so the app keeps one driver."
    private val answer = DelegatedAnswer(card, mapOf(driver to "pgx", tests to "Yes"), reason, Instant.now().minusSeconds(8 * 60))
    private val asked = "Move the importer to the new database driver"
    private val going = "The assistant picked pgx. Rewriting the importer's queries now."

    private val items = listOf(
        Item("u1", 1, role = "user", kind = "text", text = asked),
        Item("a2", 2, role = "assistant", kind = "text", text = going),
    )
    private val chats = object : ChatRepository {
        override fun observe(session: SessionKey) = MutableStateFlow(ChatSnapshot(items = items, loading = false))
        override suspend fun loadOlder(session: SessionKey) = Unit
        override suspend fun expandFold(session: SessionKey, fromSeq: Int, toSeq: Int): List<Item> = emptyList()
    }
    private val sessions = object : SessionsRepository {
        override val machines = MutableStateFlow(listOf(machine))
        override val sessions = MutableStateFlow(listOf(row))
        override suspend fun refresh() = Unit
    }
    private val sender = object : MessageSender {
        override val outbox = MutableStateFlow(emptyList<OutboxMessage>())
        override suspend fun send(session: SessionKey, text: String, clientMessageId: String, clearDraft: String?) = Unit
    }

    /** The answers the assistant gave, as the data layer keeps them; [changed]: what the user changed them to. */
    private fun interactions(changed: Map<String, String>? = null) = object : InteractionCenter {
        override val pending = MutableStateFlow(emptyList<Interaction>())
        override suspend fun refresh() = Unit
        override suspend fun resolve(id: String, resolution: Resolution) = Unit
        override val delegated = MutableStateFlow(listOf(answer))
        override val reanswered = MutableStateFlow(changed?.let { mapOf(answer.id to it) }.orEmpty())
    }

    private val actions = object : ChatActions {
        override suspend fun isMuted(session: Session) = false
        override suspend fun execute(session: Session, action: ChatAction) = ChatActionResult.Done
        override suspend fun verify(session: Session, action: ChatAction) = ChatActionResult.Done
        override suspend fun info(session: Session) = SessionInfo(sessionId = key.sessionId, cwd = "/home/me/code/app", model = "claude-opus-4-1",
            version = "2.1.0", effort = "high", contextTokens = 48_000, outputTokens = 9_600, turns = 4, gitBranch = "feat/pgx",
            startedAt = "2026-11-12T01:00:00Z")
        override suspend fun interactionHistory(session: Session) = listOf(
            InteractionRecord(card, "resolved", Resolution(answers = answer.answers, answeredBy = "assistant", reason = reason),
                answer.at.toString()))
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
        val output = File(System.getProperty("persona.shots") ?: "build/delegated-shots").also { it.mkdirs() }
        File(output, "$name-${style.name.lowercase()}-${if (dark) "dark" else "light"}.png").outputStream()
            .use { assertTrue(image.compress(Bitmap.CompressFormat.PNG, 100, it)) }
    }

    private fun chat(name: String, style: AppStyle, dark: Boolean, changed: Map<String, String>? = null) {
        rule.mainClock.autoAdvance = false
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                Box(Modifier.fillMaxSize().background(PersonaTheme.palette.background).testTag("shot")) {
                    ChatScreen(key, chats, sender, interactions(changed), sessions, onBack = {})
                }
            }
        }
        waitFor("the chat") { going in it }
        save(name, style, dark, rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap())
    }

    @Test fun cardGlassLight() = chat("card", AppStyle.Glass, false)
    @Test fun cardGlassDark() = chat("card", AppStyle.Glass, true)
    @Test fun cardBriefLight() = chat("card", AppStyle.Brief, false)
    @Test fun cardBriefDark() = chat("card", AppStyle.Brief, true)
    @Test fun cardBoardLight() = chat("card", AppStyle.Board, false)
    @Test fun cardBoardDark() = chat("card", AppStyle.Board, true)
    @Test fun changedGlassLight() = chat("card-changed", AppStyle.Glass, false, mapOf(driver to "sqlc"))
    @Test fun changedGlassDark() = chat("card-changed", AppStyle.Glass, true, mapOf(driver to "sqlc"))

    /** Change answer from the card, sqlc picked: Send takes it. */
    private fun sheet(dark: Boolean) {
        rule.mainClock.autoAdvance = false
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = AppStyle.Glass) {
                ReanswerSheet(answer, answer.answers, onDismiss = {}) { }
            }
        }
        waitFor("the sheet") { "Other answer…" in it && "sqlc" in it }
        rule.mainClock.autoAdvance = true
        rule.onNodeWithText("sqlc").performClick()
        rule.waitForIdle()
        save("reanswer", AppStyle.Glass, dark, rule.onNode(isDialog()).captureToImage().asAndroidBitmap())
    }

    // Tall, so the whole sheet shows in one picture.
    @Config(qualifiers = "en-w400dp-h1300dp-xhdpi") @Test fun sheetGlassLight() = sheet(false)
    @Config(qualifiers = "en-w400dp-h1300dp-xhdpi") @Test fun sheetGlassDark() = sheet(true)

    /** Session info's questions the assistant answered, each with Change answer (every style: rows every style shares). */
    private fun info(style: AppStyle, dark: Boolean) {
        rule.mainClock.autoAdvance = false
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                val resources = LocalResources.current
                val scope = rememberCoroutineScope()
                val chrome = remember { ChatChromeState(resources, actions, scope, {}, {}, interactions = interactions(mapOf(driver to "sqlc"))) }
                ChatSessionInfoSheet(row, machine, chrome, onDismiss = {})
            }
        }
        waitFor("the answered question") { driver in it && "Model" in it }
        save("info", style, dark, rule.onNode(isDialog()).captureToImage().asAndroidBitmap())
    }

    @Config(qualifiers = "en-w400dp-h1700dp-xhdpi") @Test fun infoGlassLight() = info(AppStyle.Glass, false)
    @Config(qualifiers = "en-w400dp-h1700dp-xhdpi") @Test fun infoGlassDark() = info(AppStyle.Glass, true)
    @Config(qualifiers = "en-w400dp-h1700dp-xhdpi") @Test fun infoBriefLight() = info(AppStyle.Brief, false)
}
