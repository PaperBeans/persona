package persona.feature.chat

import android.graphics.Bitmap
import android.graphics.Canvas
import android.graphics.Paint
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Shadows
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.ImagePayload
import persona.core.contracts.Interaction
import persona.core.contracts.Item
import persona.core.contracts.Machine
import persona.core.contracts.Resolution
import persona.core.contracts.Session
import persona.core.contracts.SessionKey
import persona.core.contracts.api.ChatRepository
import persona.core.contracts.api.ChatSnapshot
import persona.core.contracts.api.DeliveryState
import persona.core.contracts.api.InteractionCenter
import persona.core.contracts.api.MessageSender
import persona.core.contracts.api.OutboxMessage
import persona.core.contracts.api.SessionsRepository
import persona.core.designsystem.AppStyle
import persona.core.designsystem.LocalHubFeatures
import persona.core.designsystem.PersonaTheme
import java.io.File
import java.time.Instant

/**
 * The real chat page in three scenes of docs/plans/android-ios-alignment/chats.md C9 (second batch), every style, light and
 * dark: a turn whose work looked at pictures ("viewed", iOS `ViewedImagesRow`), a message that did not go and one waiting
 * for the network ("unsent", iOS `PendingBubble`), and the note after a resume turned ultracode off ("ultracode"). It uses
 * nothing new, so the same file draws `main` for the comparison. PNGs in `build/chat-details-more-shots/` (or
 * `-Dpersona.shots=<dir>`). Native JVM rendering: no system bars, no glass refraction, no device.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h760dp-xhdpi")
class ChatDetailsMoreShotsTest {
    @get:Rule val rule = createComposeRule()

    private val key = SessionKey("computer-a", "importer")
    // A fixed hour, so two runs (main and this branch) draw the same times.
    private val start = Instant.parse("2026-11-12T01:00:00Z")
    private fun at(seq: Int) = start.plusSeconds(seq * 60L).toString()
    private fun input(vararg pairs: Pair<String, String>) = buildJsonObject { pairs.forEach { (k, v) -> put(k, v) } }
    private fun png(w: Int, h: Int) = ImagePayload(mime = "image/png", lazy = true, width = w, height = h, size = w * h)
    private fun call(seq: Int, tool: String, vararg args: Pair<String, String>, image: ImagePayload? = null) = listOfNotNull(
        Item("t$seq", seq, role = "assistant", kind = "tool_use", toolUseId = "c$seq", toolName = tool, toolInput = input(*args), ts = at(seq)),
        Item("r$seq", seq, role = "user", kind = "tool_result", toolUseId = "c$seq", text = "", ts = at(seq)),
        image?.let { Item("i$seq", seq, role = "user", kind = "image", toolUseId = "c$seq", image = it, ts = at(seq)) },
    )

    private val viewed = listOf(Item("u1", 1, role = "user", kind = "text", text = "Check the sign-in page on the phone and on the web", ts = at(1))) +
        call(2, "Read", "file_path" to "/home/me/code/app/shots/sign-in.png", image = png(390, 844)) +
        call(3, "mcp__persona__computer_state", "machine" to "Studio", image = png(1440, 900)) +
        call(4, "mcp__persona__browser_look", "machine" to "Studio", image = png(1280, 800)) +
        call(5, "Read", "file_path" to "/home/me/code/app/shots/logo.png") +
        Item("a6", 6, role = "assistant", kind = "text", text = "Both pages show the new button; the web one cuts the label at narrow widths.", ts = at(6))

    private val exchange = listOf(
        Item("u1", 1, role = "user", kind = "text", text = "Make the importer retry once when the hub restarts", ts = at(1)),
        Item("a2", 2, role = "assistant", kind = "text", text = "Done. The importer now retries once after a restart.", ts = at(2)),
        Item("u3", 3, role = "user", kind = "text", text = "Run the importer tests again", ts = at(10)),
        Item("a4", 4, role = "assistant", kind = "text", text = "All 42 importer tests pass.", ts = at(11)),
    )

    private val agent = Session(key.machineId, key.sessionId, title = "Fix the importer retry", agent = "claude", status = "idle",
        cwd = "/home/me/code/app")

    /** A picture standing in for what the agent looked at: a page-coloured screen with a bar and two blocks. */
    private fun picture(item: Item, width: Int, height: Int): File {
        val w = (item.image?.width ?: 400).coerceAtMost(width.coerceAtLeast(64))
        val h = ((item.image?.height ?: 300) * w / (item.image?.width ?: 400)).coerceAtLeast(32)
        val bitmap = Bitmap.createBitmap(w, h, Bitmap.Config.ARGB_8888)
        val canvas = Canvas(bitmap)
        val hue = when (item.id) { "i2" -> 0xFFE8F0FE.toInt(); "i3" -> 0xFF2B2D33.toInt(); else -> 0xFFF4F1EA.toInt() }
        canvas.drawColor(hue)
        val paint = Paint().apply { color = 0xFF3D7BF7.toInt() }
        canvas.drawRect(0f, 0f, w.toFloat(), h * 0.08f, paint)
        paint.color = 0xFFB9C2D0.toInt()
        canvas.drawRect(w * 0.1f, h * 0.25f, w * 0.9f, h * 0.4f, paint)
        canvas.drawRect(w * 0.1f, h * 0.5f, w * 0.6f, h * 0.6f, paint)
        val file = File.createTempFile("viewed-${item.id}", ".png")
        file.outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
        return file
    }

    private fun texts(): List<String> = rule.onAllNodes(SemanticsMatcher("any node") { true }, useUnmergedTree = true).fetchSemanticsNodes().flatMap { node ->
        node.config.getOrNull(SemanticsProperties.Text).orEmpty().map { it.text } +
            node.config.getOrNull(SemanticsProperties.ContentDescription).orEmpty()
    }

    private fun scene(name: String, style: AppStyle, dark: Boolean) {
        val items = if (name == "viewed") viewed else exchange
        val session = if (name == "ultracode") agent.copy(ultracode = false, ultracodeLostAt = at(9)) else agent
        val outbox = if (name == "unsent") listOf(
            OutboxMessage("m1", key, "Also add a line to the release notes", DeliveryState.Refused, error = "dialog_open"),
            OutboxMessage("m2", key, "And bump the version", DeliveryState.WaitingForNetwork),
        ) else emptyList()
        val sessions = object : SessionsRepository {
            override val machines = MutableStateFlow(listOf(Machine(key.machineId, "Computer A", online = true)))
            override val sessions = MutableStateFlow(listOf(session))
            override suspend fun refresh() = Unit
        }
        val chats = object : ChatRepository, ChatLocalStorage {
            override fun observe(session: SessionKey) = MutableStateFlow(ChatSnapshot(items = items, loading = false))
            override suspend fun loadOlder(session: SessionKey) = Unit
            override suspend fun expandFold(session: SessionKey, fromSeq: Int, toSeq: Int): List<Item> = emptyList()
            override val localStorage = MutableStateFlow<ChatStorageSnapshot?>(null)
            override suspend fun imageFile(session: SessionKey, item: Item, width: Int, height: Int) = picture(item, width, height)
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
        rule.mainClock.autoAdvance = false // spinners never settle
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                CompositionLocalProvider(LocalHubFeatures provides setOf("ultracode")) {
                    Box(Modifier.fillMaxSize().background(PersonaTheme.palette.background).testTag("shot")) {
                        ChatScreen(key, chats, sender, interactions, sessions, onBack = {})
                    }
                }
            }
        }
        // Lines are built off the main thread and pictures load on another: frames by hand, real time until they show.
        val ready: (List<String>) -> Boolean = when (name) {
            "viewed" -> { shown -> shown.any { it.startsWith("Both pages show") } }
            "unsent" -> { shown -> "And bump the version" in shown }
            else -> { shown -> shown.any { it.startsWith("ultracode off") } }
        }
        val deadline = System.currentTimeMillis() + 30_000
        while (true) {
            Snapshot.sendApplyNotifications()
            rule.mainClock.advanceTimeByFrame()
            if (ready(texts())) break
            check(System.currentTimeMillis() < deadline) { "$name never showed; shown: ${texts()}" }
            Thread.sleep(20)
        }
        repeat(60) {
            Shadows.shadowOf(android.os.Looper.getMainLooper()).idle()
            Snapshot.sendApplyNotifications()
            rule.mainClock.advanceTimeByFrame()
            Thread.sleep(20)
        }
        val image = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
        val output = File(System.getProperty("persona.shots") ?: "build/chat-details-more-shots").also { it.mkdirs() }
        File(output, "$name-${style.name.lowercase()}-${if (dark) "dark" else "light"}.png").outputStream()
            .use { assertTrue(image.compress(Bitmap.CompressFormat.PNG, 100, it)) }
    }

    @Test fun viewedGlassLight() = scene("viewed", AppStyle.Glass, false)
    @Test fun viewedGlassDark() = scene("viewed", AppStyle.Glass, true)
    @Test fun viewedBriefLight() = scene("viewed", AppStyle.Brief, false)
    @Test fun viewedBoardDark() = scene("viewed", AppStyle.Board, true)
    @Test fun unsentGlassLight() = scene("unsent", AppStyle.Glass, false)
    @Test fun unsentGlassDark() = scene("unsent", AppStyle.Glass, true)
    @Test fun unsentBriefLight() = scene("unsent", AppStyle.Brief, false)
    @Test fun unsentBoardDark() = scene("unsent", AppStyle.Board, true)
    @Test fun ultracodeGlassLight() = scene("ultracode", AppStyle.Glass, false)
    @Test fun ultracodeGlassDark() = scene("ultracode", AppStyle.Glass, true)
    @Test fun ultracodeBriefLight() = scene("ultracode", AppStyle.Brief, false)
    @Test fun ultracodeBoardDark() = scene("ultracode", AppStyle.Board, true)
}
