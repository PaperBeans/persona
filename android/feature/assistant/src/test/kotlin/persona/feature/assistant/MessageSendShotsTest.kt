package persona.feature.assistant

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.*
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.unit.Density
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.navigation3.runtime.NavKey
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.*
import persona.core.contracts.api.*
import persona.core.data.*
import persona.core.designsystem.*
import persona.core.navigation.AppTab
import persona.core.navigation.Navigator
import persona.feature.chat.ChatRenderStyle
import persona.feature.chat.ChatScreen
import java.io.File
import java.time.Instant

/** Actual page clicks with gated local storage and HTTP, not device screenshots or production latency measurements. */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "zh-w400dp-h900dp-xhdpi")
class MessageSendShotsTest {
    @get:Rule val rule = createComposeRule()
    private val navigator = object : Navigator {
        override fun navigate(key: NavKey) = Unit
        override fun back() = Unit
        override fun selectTab(tab: AppTab) = Unit
    }
    private class Store : OutboxStore {
        override val entries = MutableStateFlow<List<OutboxEntry>>(emptyList())
        private var main: SessionKey? = null
        override suspend fun enqueue(entry: OutboxEntry) { entries.value += entry }
        override suspend fun mainConversation() = main
        override suspend fun claimMainCreation() = true
        override suspend fun bindMainConversation(session: SessionKey) {
            main = session
            entries.value = entries.value.map { if (it.mainTarget) it.copy(session = session) else it }
        }
        override suspend fun transition(id: String, from: Set<OutboxState>, to: OutboxState, error: String?): Boolean {
            val old = entries.value.firstOrNull { it.clientMessageId == id && it.state in from } ?: return false
            entries.value = entries.value.map { if (it == old) it.copy(state = to, error = error) else it }
            return true
        }
        override suspend fun recoverInterrupted() = Unit
    }
    private interface Sender : MessageSender, MainConversationSender
    private class Fixture(val assistantPage: Boolean, generation: String = "shots") {
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        val key = SessionKey("computer", if (assistantPage) "main" else "agent")
        val store = Store()
        val enqueue = CompletableDeferred<Unit>()
        val posted = CompletableDeferred<MessageRequest>()
        val http = CompletableDeferred<MessageTransportResult>()
        val events = MutableSharedFlow<ServerEvent>(extraBufferCapacity = 8)
        val transport = object : MessageTransport {
            override val readyToSend = MutableStateFlow(true)
            override val supportsClientOps = false
            override suspend fun send(request: MessageRequest): MessageTransportResult {
                posted.complete(request)
                return http.await()
            }
        }
        val sender = OutboxMessageSender(HubScope("hub", generation), store, transport, scope)
        val messages: Sender = object : Sender {
            override val outbox = sender.outbox
            override val sendReceipts = sender.sendReceipts
            override suspend fun send(session: SessionKey, text: String, clientMessageId: String, clearDraft: String?) {
                enqueue.await()
                sender.send(session, text, clientMessageId, clearDraft)
            }
            override suspend fun sendToMain(text: String, clientMessageId: String, draft: DraftSnapshot?) {
                enqueue.await()
                sender.sendToMain(text, clientMessageId, draft)
            }
        }
        val row = Session(key.machineId, key.sessionId, title = if (assistantPage) "安排周末" else "整理项目", status = "idle", assistant = assistantPage)
        val runtime = MutableStateFlow(listOf(row))
        val sessions = object : SessionsRepository {
            override val machines = MutableStateFlow(listOf(Machine("computer", name = "我的电脑", online = true)))
            override val sessions = runtime
            override suspend fun refresh() = Unit
        }
        val chat = MutableStateFlow(ChatSnapshot(loading = false))
        val chats = object : ChatRepository {
            override fun observe(session: SessionKey) = chat
            override suspend fun loadOlder(session: SessionKey) = Unit
            override suspend fun expandFold(session: SessionKey, fromSeq: Int, toSeq: Int) = emptyList<Item>()
        }
        val interactions = object : InteractionCenter {
            override val pending = MutableStateFlow(emptyList<Interaction>())
            override suspend fun refresh() = Unit
            override suspend fun resolve(id: String, resolution: Resolution) = Unit
        }
        val assistant = object : AssistantRepository {
            override val assistant = MutableStateFlow(AssistantSnapshot(mainSession = key, conversations = listOf(row)))
            override suspend fun refresh() = Unit
        }
        init {
            if (System.getenv("PERSONA_SEND_EVIDENCE_PHASE")?.startsWith("baseline") != true) {
                val connection = object : HubConnection {
                    override val state = MutableStateFlow(ConnectionState.Online)
                    // Synthetic authenticated-hello capability, not a live transport negotiation claim.
                    override val lastHello = Hello(hubId = "hub", features = listOf("message_send_state"), messageSendState = 1)
                    override val currentHello = MutableStateFlow<SocketHello?>(SocketHello(1, lastHello))
                    override val events = this@Fixture.events
                    override fun reconnect() = Unit
                    override fun send(message: kotlinx.serialization.json.JsonObject) = true
                }
                sender.connectSendReceipts(connection, runtime)
            }
            runBlocking { sender.connectMainConversation(object : MainConversationResolver {
                override suspend fun findMainConversation() = key
                override suspend fun createMainConversation() = key
            }) }
        }
        fun close() { scope.cancel() }
    }

    @Test fun subscribedRemoteReceiptsDoNotCrossAReplacementHubWithIdenticalSessionIds() {
        val first = Fixture(false, "first")
        val second = Fixture(false, "second")
        var current by mutableStateOf(first)
        rule.mainClock.autoAdvance = false
        rule.setContent { ChatScreen(current.key, current.chats, current.messages, current.interactions, current.sessions, onBack = {}) }
        fun settle() { Snapshot.sendApplyNotifications(); repeat(4) { rule.mainClock.advanceTimeByFrame() } }
        fun visible() = rule.onAllNodesWithText("处理中").fetchSemanticsNodes().isNotEmpty()
        fun awaitProcessing() {
            val until = System.nanoTime() + 10_000_000_000L
            do { settle(); if (visible()) return; Thread.sleep(10) } while (System.nanoTime() < until)
            fail("remote receipt never appeared")
        }
        fun receipt(fixture: Fixture, id: String, state: String = "processing") = MessageSendStateEvent(
            fixture.key.machineId, fixture.key.sessionId, id, state, Instant.now().toString(), socketGeneration = 1)
        try {
            settle()
            first.events.tryEmit(receipt(first, "remote"))
            awaitProcessing()
            rule.onNodeWithText("处理中").assertIsDisplayed()
            assertTrue(first.sender.outbox.value.isEmpty())
            assertEquals("idle", first.runtime.value.single().status)
            rule.runOnIdle { current = second }
            settle()
            assertFalse("a new hub must not inherit the prior flow's last value", visible())
            first.events.tryEmit(receipt(first, "another-old-hub"))
            settle()
            assertFalse(visible())
            second.events.tryEmit(receipt(second, "remote"))
            awaitProcessing()
            second.events.tryEmit(receipt(second, "remote", "failed"))
            val until = System.nanoTime() + 10_000_000_000L
            do { settle(); if (!visible()) break; Thread.sleep(10) } while (System.nanoTime() < until)
            assertFalse(visible())
            assertTrue(second.sender.outbox.value.isEmpty())
            assertEquals("idle", second.runtime.value.single().status)
        } finally { first.close(); second.close() }
    }

    @Test fun clickedPagesInEveryStyleAndAppearance() {
        val phase = System.getenv("PERSONA_SEND_EVIDENCE_PHASE") ?: "after"
        val directory = File("/tmp/persona-5258-android-$phase").also { it.mkdirs() }
        var current by mutableStateOf<Fixture?>(null)
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        var fontScale by mutableFloatStateOf(1f)
        val backgroundPixels = mutableMapOf<String, Int>()
        rule.mainClock.autoAdvance = false
        AssistantManagerChoice.choose("hub", null, "assistant")
        rule.setContent {
            val density = LocalDensity.current
            val configuration = android.content.res.Configuration(LocalConfiguration.current).apply {
                this.fontScale = fontScale
                uiMode = (uiMode and android.content.res.Configuration.UI_MODE_NIGHT_MASK.inv()) or
                    if (dark) android.content.res.Configuration.UI_MODE_NIGHT_YES else android.content.res.Configuration.UI_MODE_NIGHT_NO
            }
            CompositionLocalProvider(LocalDensity provides Density(density.density, fontScale), LocalConfiguration provides configuration) {
            PersonaTheme(style = style, darkTheme = dark, reduceMotion = true) {
                Box(Modifier.fillMaxSize().testTag("send-page").background(PersonaTheme.palette.background)) {
                    current?.let { fixture -> key(fixture) {
                        val machines by fixture.sessions.machines.collectAsState()
                        if (fixture.assistantPage) AssistantScreen(navigator, fixture.assistant, fixture.messages, { fixture.key },
                            mainConversationSender = fixture.messages, composerService = null, machines = machines,
                            assistantChatContent = { session, _ -> ChatScreen(session, fixture.chats, fixture.messages, fixture.interactions,
                                fixture.sessions, onBack = {}, renderStyle = ChatRenderStyle.Assistant, embedded = true, showTopBar = false) })
                        else ChatScreen(fixture.key, fixture.chats, fixture.messages, fixture.interactions, fixture.sessions, onBack = {})
                    } }
                }
            }
            }
        }
        fun settle() { Snapshot.sendApplyNotifications(); repeat(4) { rule.mainClock.advanceTimeByFrame() } }
        fun texts() = rule.onAllNodes(SemanticsMatcher("any node") { true }, useUnmergedTree = true).fetchSemanticsNodes().flatMap {
            it.config.getOrNull(SemanticsProperties.Text).orEmpty().map { text -> text.text } +
                it.config.getOrNull(SemanticsProperties.ContentDescription).orEmpty()
        }
        fun waitFor(description: String, condition: () -> Boolean) {
            val until = System.nanoTime() + 15_000_000_000L
            do { settle(); if (condition()) return; Thread.sleep(10) } while (System.nanoTime() < until)
            fail("$description: ${texts()}")
        }
        try {
            for (home in listOf(true, false)) for (s in AppStyle.entries) for (d in listOf(false, true))
                for (scale in if (s == AppStyle.Glass && !phase.startsWith("baseline")) listOf(1f, 2f) else listOf(1f)) {
                if (phase == "baseline-working" && s != AppStyle.Glass) continue
                val fixture = Fixture(home)
                val prefix = "${if (home) "assistant" else "agent"}-${s.name.lowercase()}-${if (d) "dark" else "light"}${if (scale > 1f) "-large" else ""}"
                rule.runOnIdle { current?.close(); current = fixture; style = s; dark = d; fontScale = scale }
                waitFor("composer") { rule.onAllNodes(hasSetTextAction()).fetchSemanticsNodes().isNotEmpty() }
                rule.onNode(hasSetTextAction()).performTextReplacement("请继续处理这件事")
                settle()
                val started = System.nanoTime()
                rule.onNodeWithContentDescription("发送").performClick()
                settle()
                fun capture(step: String) {
                    val values = texts()
                    if (!phase.startsWith("baseline") && step == "clicked") assertTrue(values.toString(), "发送中" in values)
                    if (!phase.startsWith("baseline") && step in setOf("receipt", "echo", "delivered")) {
                        assertTrue(values.toString(), "处理中" in values)
                        assertEquals("receipts must not unlock Stop", "idle", fixture.runtime.value.single().status)
                        assertFalse(values.toString(), values.any { it == "停止" || it == "中断" })
                    }
                    File(directory, "$prefix-$step.txt").writeText("elapsed_ms=${(System.nanoTime() - started) / 1_000_000}\ncompose_clock_ms=${rule.mainClock.currentTime}\nfont_scale=$scale\n" + values.joinToString("\n"))
                    val bitmap = rule.onNodeWithTag("send-page").captureToImage().asAndroidBitmap()
                    val colorKey = "$home-$s-$scale-$step"
                    val pixel = bitmap.getPixel(bitmap.width / 2, bitmap.height / 2)
                    if (d) assertNotEquals("dark and light must differ", backgroundPixels[colorKey], pixel)
                    else backgroundPixels[colorKey] = pixel
                    File(directory, "$prefix-$step.png").outputStream().use { assertTrue(bitmap.compress(Bitmap.CompressFormat.PNG, 100, it)) }
                }
                capture("clicked")
                fixture.enqueue.complete(Unit)
                waitFor("HTTP started") { fixture.posted.isCompleted }
                val request = runBlocking { fixture.posted.await() }
                ServerEvent.decode("""{"type":"message_send_state","machine_id":"computer","session_id":"${fixture.key.sessionId}","client_msg_id":"${request.opId}","state":"processing","at":"${Instant.now()}"}""")?.let { fixture.events.tryEmit(if (it is MessageSendStateEvent) it.copy(socketGeneration = 1) else it) }
                settle()
                if (!phase.startsWith("baseline")) waitFor("hub receipt") { "处理中" in texts() }
                capture("receipt")
                if (!phase.startsWith("baseline")) {
                    val user = Item("runtime-user", 1, role = "user", kind = "text", text = "请继续处理这件事",
                        clientMsgId = request.opId, ts = Instant.now().toString())
                    runBlocking { fixture.sender.observeTranscript(fixture.key, listOf(user)) }
                    fixture.chat.value = ChatSnapshot(items = listOf(user), loading = false)
                    waitFor("echo mounted after removing durable bubble") {
                        fixture.sender.outbox.value.isEmpty() &&
                            rule.onAllNodesWithText("请继续处理这件事").fetchSemanticsNodes().isNotEmpty()
                    }
                    settle()
                    capture("echo")
                }
                fixture.http.complete(MessageTransportResult.Accepted)
                if (phase.startsWith("baseline")) waitFor("HTTP final") { fixture.store.entries.value.any { it.state == OutboxState.Sent } }
                else runBlocking { withTimeout(5_000) { fixture.sender.onReady() } }
                settle()
                capture("delivered")
                if (phase == "baseline-working") {
                    waitFor("delayed authority") { System.nanoTime() - started >= 10_000_000_000L }
                    fixture.runtime.value = listOf(fixture.row.copy(status = "working", state = "working", stateSince = Instant.now().toString()))
                    settle()
                    capture("authority-working")
                }
                fixture.close()
            }
        } finally { current?.close(); AssistantManagerChoice.reset() }
    }
}
