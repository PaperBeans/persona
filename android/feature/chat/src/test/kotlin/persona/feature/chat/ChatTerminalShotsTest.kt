package persona.feature.chat

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
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
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
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
 * An Agent's chat while it works, with a terminal to open (#4339 C7): the header (no terminal button on Glass, Brief and
 * Board as they were), the live work line (its terminal mark on Glass) in every style, light and dark; and ⋯'s sheet on
 * Glass, light and dark, which lists Terminal first. PNGs in `build/terminal-shots/` (or `-Dpersona.shots=<dir>`).
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h900dp-xhdpi")
class ChatTerminalShotsTest {
    @get:Rule val rule = createComposeRule()

    private fun out(): File = File(System.getProperty("persona.shots") ?: "build/terminal-shots").also { it.mkdirs() }

    private val key = SessionKey("macbook", "weather")
    private val start = Instant.now().minusSeconds(95)
    private val items = listOf(
        Item("u1", 1, role = "user", kind = "text", text = "Add a home screen widget that shows today's forecast.", ts = start.toString()),
        Item("a2", 2, role = "assistant", kind = "text", text = "I'll add the widget target first, then run the tests.",
            ts = start.plusSeconds(3).toString()),
        Item("t3", 3, role = "assistant", kind = "tool_use", toolUseId = "t3", toolName = "Bash",
            toolInput = buildJsonObject { put("command", "swift test") }, ts = start.plusSeconds(5).toString()),
    )
    private val sessions = object : SessionsRepository {
        override val machines = MutableStateFlow(listOf(Machine("macbook", "MacBook", online = true)))
        override val sessions = MutableStateFlow(listOf(Session(machineId = key.machineId, sessionId = key.sessionId,
            title = "Weather widget", status = "working", state = "working", agent = "claude", paneId = "p1",
            cwd = "/Users/me/weather", spaceName = "weather", slotName = "widget",
            stateSince = start.toString())))
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
    private val clock = Regex("\\d+m \\d+s")

    private fun texts(): List<String> = rule.onAllNodes(SemanticsMatcher("any node") { true }, useUnmergedTree = true)
        .fetchSemanticsNodes().flatMap { node ->
            node.config.getOrNull(SemanticsProperties.Text).orEmpty().map { it.text } +
                node.config.getOrNull(SemanticsProperties.ContentDescription).orEmpty()
        }

    /** The clock and the spinner never settle: frames by hand, real time for the lines built off the main thread. */
    private fun settle() {
        val deadline = System.currentTimeMillis() + 60_000
        while (texts().none(clock::containsMatchIn)) {
            rule.mainClock.advanceTimeByFrame()
            check(System.currentTimeMillis() < deadline) { "the work line never showed: ${texts()}" }
            Thread.sleep(20)
        }
        Snapshot.sendApplyNotifications()
        repeat(4) { rule.mainClock.advanceTimeByFrame() }
    }

    private fun save(bitmap: Bitmap, name: String) =
        File(out(), "$name.png").outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }

    @Test fun thePageInEveryStyle() {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        rule.mainClock.autoAdvance = false
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                Box(Modifier.fillMaxSize().background(PersonaTheme.palette.background).testTag("page")) {
                    ChatScreen(key, chats, sender, interactions, sessions, onBack = {}, onTerminal = {})
                }
            }
        }
        for (s in AppStyle.entries) for (d in listOf(false, true)) {
            style = s; dark = d
            settle()
            save(rule.onNodeWithTag("page").captureToImage().asAndroidBitmap(), "chat-terminal-${s.name.lowercase()}-${if (d) "dark" else "light"}")
        }
    }

    /**
     * A sheet keeps its first frame after a theme change: each look is a test of its own. The agent is idle here, so
     * nothing ticks and the sheet can settle on the test's own clock.
     */
    private fun menu(dark: Boolean) {
        sessions.sessions.value = sessions.sessions.value.map { it.copy(status = "idle", state = "idle") }
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = AppStyle.Glass) {
                ChatScreen(key, chats, sender, interactions, sessions, onBack = {}, onTerminal = {})
            }
        }
        rule.waitForIdle()
        rule.onNodeWithContentDescription("More actions").performClick()
        rule.waitForIdle()
        save(rule.onNode(isDialog()).captureToImage().asAndroidBitmap(), "chat-terminal-menu-glass-${if (dark) "dark" else "light"}")
    }

    @Test fun theMenuLight() = menu(dark = false)

    @Test fun theMenuDark() = menu(dark = true)
}
