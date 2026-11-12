package persona.feature.chat

import android.content.res.Configuration
import android.graphics.Bitmap
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.test.assertIsNotDisplayed
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.hasScrollToIndexAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performScrollToIndex
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.MutableStateFlow
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
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
 * #2769: the real chat page with a failed send above the message written after it (the replay case
 * `send_failed_stays_above_later`). Rows no longer line up with lines by position once a bubble sits between them, so
 * the page maps the rows on screen to lines by key: a reply scrolled off below is not reported as seen. The page in
 * every style, light and dark, goes to `build/failed-send-shots/` (or `-Dpersona.shots=<dir>`).
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h900dp-xhdpi")
class FailedSendRowsTest {
    @get:Rule val rule = createComposeRule()

    private val key = SessionKey("macbook", "weekend")
    private val start = Instant.parse("2026-01-02T03:04:05Z")
    private fun at(seconds: Long) = start.plusSeconds(seconds)
    private val rain = "Rain plan: the aquarium opens at ten. " + (1..30).joinToString(" ") { "Step $it of the rainy day." }
    private val longBackup = "And a backup plan for rain. " + (1..12).joinToString(" ") { "Keep option $it indoors, near a station." }
    // An earlier exchange tall enough that the list can scroll the failed bubble down to its bottom edge.
    private val earlier = listOf(
        Item("l0#0", 1, role = "user", kind = "text", text = "What is the weather this weekend?", ts = at(-600).toString()),
        Item("l0#1", 2, role = "assistant", kind = "text", ts = at(-580).toString(),
            text = "Sunny on Saturday, rain likely on Sunday. " + (1..16).joinToString(" ") { "Hour $it: mild, light wind." }),
    )
    private fun items(rain: String, backup: String = "And a backup plan for rain.", before: List<Item> = emptyList()) = before + listOf(
        Item("l1#0", 3, role = "user", kind = "text", text = "Plan a weekend trip.", ts = at(0).toString()),
        Item("l2#0", 4, role = "assistant", kind = "text", text = "Saturday hike, Sunday museum.", ts = at(20).toString()),
        Item("l4#0", 6, role = "user", kind = "text", text = backup, clientMsgId = "m2", ts = at(120).toString()),
        Item("l5#0", 7, role = "assistant", kind = "text", text = rain, ts = at(140).toString()),
    )
    // "Find a dinner place." went out at 1 min and the computer refused it; "And a backup plan for rain." came after.
    private val failed = OutboxMessage("m1", key, "Find a dinner place.", DeliveryState.Refused, error = "dialog_open",
        createdAt = at(60).toEpochMilli())

    private val sessions = object : SessionsRepository {
        override val machines = MutableStateFlow(emptyList<Machine>())
        override val sessions = MutableStateFlow(listOf(Session(machineId = key.machineId, sessionId = key.sessionId,
            title = "Weekend trip", status = "idle")))
        override suspend fun refresh() = Unit
    }
    private val snapshot = MutableStateFlow(ChatSnapshot(items = items(rain, longBackup, earlier), loading = false))
    private val seen = mutableListOf<String>()
    private val chats = object : ChatRepository {
        override fun observe(session: SessionKey) = snapshot
        override suspend fun loadOlder(session: SessionKey) = Unit
        override suspend fun expandFold(session: SessionKey, fromSeq: Int, toSeq: Int): List<Item> = emptyList()
        override suspend fun markSeen(session: SessionKey, itemId: String) { seen += itemId }
    }
    private val sender = object : MessageSender {
        override val outbox = MutableStateFlow(listOf(failed))
        override val writtenAt = MutableStateFlow(mapOf("m1" to at(60).toEpochMilli(), "m2" to at(118).toEpochMilli()))
        override suspend fun send(session: SessionKey, text: String, clientMessageId: String, clearDraft: String?) = Unit
    }
    private val interactions = object : InteractionCenter {
        override val pending = MutableStateFlow(emptyList<Interaction>())
        override suspend fun refresh() = Unit
        override suspend fun resolve(id: String, resolution: Resolution) = Unit
    }

    /** Lines are built off the main thread: frames by hand, real time in between. */
    private fun waitFor(what: String, condition: () -> Boolean) {
        val deadline = System.currentTimeMillis() + 60_000
        while (!condition()) {
            check(System.currentTimeMillis() < deadline) { "$what never happened" }
            rule.mainClock.advanceTimeBy(100)
            Thread.sleep(20)
        }
    }

    private fun shown(text: String) = rule.onAllNodes(hasText(text, substring = true))
        .fetchSemanticsNodes().isNotEmpty()

    @Test fun aReplyScrolledOffBelowIsNotReportedSeen() {
        rule.mainClock.autoAdvance = false
        snapshot.value = snapshot.value.copy(verified = false) // no read receipts until the list is where the test wants it
        rule.setContent { ChatScreen(key, chats, sender, interactions, sessions, onBack = {}) }
        waitFor("the chat") { shown("And a backup plan for rain.") && shown("Find a dinner place.") }

        // Rows, newest first: the rain reply, the (long) rain message, the failed bubble, then the first exchange. Put the
        // failed bubble at the list's bottom edge: the rain message fills the padding under it (behind the composer), and
        // the reply below that is off screen. Counting rows by position took the rain message's row for the reply.
        rule.onNode(hasScrollToIndexAction()).performScrollToIndex(2)
        rule.mainClock.advanceTimeBy(500)
        rule.onNodeWithText("Rain plan:", substring = true).assertIsNotDisplayed()

        snapshot.value = snapshot.value.copy(verified = true)
        waitFor("a read receipt") { seen.isNotEmpty() }
        rule.mainClock.advanceTimeBy(2_000)
        assertFalse("the reply off screen was reported seen: $seen", "l5#0" in seen)
        assertTrue("the newest line on screen was not reported seen: $seen", "l4#0" in seen)
    }

    @Test fun shots() {
        // A short reply: the whole chat fits, so the failed bubble's place shows without scrolling.
        snapshot.value = ChatSnapshot(items = items("Rain plan: the aquarium opens at ten."), loading = false)
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        rule.mainClock.autoAdvance = false
        rule.setContent {
            // As the app's own window: the system's night mode and the theme's dark flag change together.
            val configuration = Configuration(LocalConfiguration.current).apply {
                uiMode = (uiMode and Configuration.UI_MODE_NIGHT_MASK.inv()) or
                    if (dark) Configuration.UI_MODE_NIGHT_YES else Configuration.UI_MODE_NIGHT_NO
            }
            CompositionLocalProvider(LocalConfiguration provides configuration) {
                PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                    ChatScreen(key, chats, sender, interactions, sessions, onBack = {})
                }
            }
        }
        waitFor("the chat") { shown("And a backup plan for rain.") && shown("Find a dinner place.") }
        val out = File(System.getProperty("persona.shots") ?: "build/failed-send-shots").also { it.mkdirs() }
        for (s in AppStyle.entries) {
            val grounds = mutableListOf<Int>() // the empty page under the chat, light then dark
            for (d in listOf(false, true)) {
                style = s; dark = d
                Snapshot.sendApplyNotifications() // written on the test thread: tell the page before its frames run
                rule.mainClock.advanceTimeBy(1_500)
                val bitmap = rule.onRoot().captureToImage().asAndroidBitmap()
                grounds += bitmap.getPixel(bitmap.width / 2, bitmap.height * 3 / 4)
                File(out, "failed-send-${s.name.lowercase()}-${if (d) "dark" else "light"}.png").outputStream()
                    .use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
            }
            assertNotEquals("${s.name}: light and dark captures must differ", grounds[0], grounds[1])
        }
    }
}
