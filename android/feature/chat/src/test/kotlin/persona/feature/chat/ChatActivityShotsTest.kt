package persona.feature.chat

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasContentDescription
import androidx.compose.ui.test.hasScrollToIndexAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.isDialog
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTouchInput
import androidx.compose.ui.test.swipeDown
import androidx.compose.ui.test.swipeUp
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.MutableStateFlow
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.BackgroundTask
import persona.core.contracts.Interaction
import persona.core.contracts.Item
import persona.core.contracts.LoopInfo
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
import java.time.temporal.ChronoUnit

/**
 * An Agent's chat running a /loop with background work (#4339 C8): the page in every style, light and dark (Glass: the
 * pill at the end of the tabs' row; without tabs, a row of its own; folded, the slim bar), and what the pill (on `main`,
 * the Loop chip) opens on Glass. Animations off. PNGs in `build/activity-shots/` (or `-Dpersona.shots=<dir>`).
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h900dp-xhdpi")
class ChatActivityShotsTest {
    @get:Rule val rule = createComposeRule()

    private fun out(): File = File(System.getProperty("persona.shots") ?: "build/activity-shots").also { it.mkdirs() }

    private val key = SessionKey("macbook", "ci")
    private val start = Instant.parse("2026-11-10T09:00:00Z") // fixed: the times under the bubbles match between runs
    private val said = listOf(
        "/loop 30m check the CI of the open pull requests and fix what is clearly ours",
        "Looping every 30 minutes. This round: two pull requests are green, one failed its lint step; I'm fixing it.",
        "Also run the integration tests in the background.",
        "Started them in the background. The lint fix is pushed; the next check is in 30 minutes.",
    )
    private val items = (1..16).map { n ->
        Item("i$n", n, role = if (n % 2 == 1) "user" else "assistant", kind = "text", text = said[(n - 1) % said.size],
            ts = start.plusSeconds(n * 40L).toString())
    }
    private val now = Instant.now().truncatedTo(ChronoUnit.MINUTES)
    private val base = Session(machineId = key.machineId, sessionId = key.sessionId, title = "Watch CI", status = "idle",
        state = "idle", agent = "claude", paneId = "p1", cwd = "/Users/me/ci", spaceName = "ci", spaceId = "w1", slotName = "watch",
        loop = LoopInfo(kind = "wakeup", prompt = "/loop 30m check the CI of the open pull requests and fix what is clearly ours",
            intervalSeconds = 1800, nextAt = now.plusSeconds(12 * 60).toString(), reason = "Waiting for the next CI run"),
        background = listOf(
            BackgroundTask("b1", "shell", description = "Run the integration tests", command = "make integration",
                outputFile = "/tmp/tasks/b1.output", startedAt = now.minusSeconds(185).toString()),
            BackgroundTask("b2", "shell", command = "tail -f build/ci.log", startedAt = now.minusSeconds(42 * 60).toString()),
        ))
    private val sibling = base.copy(sessionId = "docs", title = "Update the docs", paneId = "p2", slotName = "docs",
        loop = null, background = null)
    private val sessions = object : SessionsRepository {
        override val machines = MutableStateFlow(listOf(Machine("macbook", "MacBook", online = true)))
        override val sessions = MutableStateFlow(listOf(base, sibling))
        override suspend fun refresh() = Unit
    }
    private val chats = object : ChatRepository {
        override fun observe(session: SessionKey) = MutableStateFlow(ChatSnapshot(items = items, loading = false))
        override suspend fun loadOlder(session: SessionKey) = Unit
        override suspend fun expandFold(session: SessionKey, fromSeq: Int, toSeq: Int): List<Item> = emptyList()
    }
    private val sender = object : MessageSender {
        override val outbox = MutableStateFlow(emptyList<OutboxMessage>())
        override suspend fun send(session: SessionKey, text: String, clientMessageId: String, clearDraft: String?) = Unit
    }
    private val interactions = object : InteractionCenter {
        override val pending = MutableStateFlow(emptyList<Interaction>())
        override suspend fun refresh() = Unit
        override suspend fun resolve(id: String, resolution: Resolution) = Unit
    }
    private val renamer = object : persona.core.domain.sort.SessionRenamer {
        override fun project(source: kotlinx.coroutines.flow.Flow<List<Session>>) = source
        override suspend fun rename(session: Session, title: String?, tabName: String?) = true
    }

    private fun animationsOff() {
        val context = androidx.test.core.app.ApplicationProvider.getApplicationContext<android.content.Context>()
        android.provider.Settings.Global.putFloat(context.contentResolver, android.provider.Settings.Global.ANIMATOR_DURATION_SCALE, 0f)
    }

    private fun save(bitmap: Bitmap, name: String) =
        File(out(), "$name.png").outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }

    private fun page(style: AppStyle, dark: Boolean, tabs: Boolean = true) {
        animationsOff()
        if (!tabs) sessions.sessions.value = listOf(base)
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                Box(Modifier.fillMaxSize().background(PersonaTheme.palette.background).testTag("page")) {
                    ChatScreen(key, chats, sender, interactions, sessions, onBack = {},
                        chrome = ChatChromeBindings(renamer = renamer, onOpenSession = {}, onOpenPath = { _, _ -> }))
                }
            }
        }
        rule.waitUntil(20_000) { rule.onAllNodesWithText(said.last(), substring = true).fetchSemanticsNodes().isNotEmpty() }
        rule.waitForIdle()
    }

    private fun name(style: AppStyle, dark: Boolean) = "${style.name.lowercase()}-${if (dark) "dark" else "light"}"

    private fun shot(name: String) = save(rule.onNodeWithTag("page").captureToImage().asAndroidBitmap(), name)

    private fun everyStyle(style: AppStyle, dark: Boolean) {
        page(style, dark)
        shot("activity-page-${name(style, dark)}")
        if (style != AppStyle.Glass) return
        val list = rule.onNode(hasScrollToIndexAction() and SemanticsMatcher.keyIsDefined(SemanticsProperties.VerticalScrollAxisRange))
        list.performTouchInput { swipeDown() }
        rule.waitForIdle()
        list.performTouchInput { swipeUp(endY = top + height * 0.35f) }
        rule.waitForIdle()
        shot("activity-scrolled-${name(style, dark)}")
    }

    @Test fun glassLight() = everyStyle(AppStyle.Glass, dark = false)
    @Test fun glassDark() = everyStyle(AppStyle.Glass, dark = true)
    @Test fun briefLight() = everyStyle(AppStyle.Brief, dark = false)
    @Test fun briefDark() = everyStyle(AppStyle.Brief, dark = true)
    @Test fun boardLight() = everyStyle(AppStyle.Board, dark = false)
    @Test fun boardDark() = everyStyle(AppStyle.Board, dark = true)

    /** No other tab in the space: on Glass the pill is a row of its own, at its end. */
    @Test fun glassWithoutTabs() {
        page(AppStyle.Glass, dark = false, tabs = false)
        shot("activity-page-glass-light-no-tabs")
    }

    /** What opens it on Glass: the pill's sheet (on `main`, the Loop chip's status sheet). A sheet keeps its first frame: a test each. */
    private fun sheet(dark: Boolean) {
        page(AppStyle.Glass, dark)
        val pill = hasContentDescription("Looping, ", substring = true) and hasClickAction()
        if (rule.onAllNodes(pill).fetchSemanticsNodes().isNotEmpty()) rule.onNode(pill).performClick()
        else rule.onNode(hasText("Loop", substring = true) and hasClickAction()).performClick()
        rule.waitForIdle()
        save(rule.onNode(isDialog()).captureToImage().asAndroidBitmap(), "activity-sheet-glass-${if (dark) "dark" else "light"}")
    }

    @Test fun sheetLight() = sheet(dark = false)
    @Test fun sheetDark() = sheet(dark = true)
}
