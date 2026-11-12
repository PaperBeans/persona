package persona.feature.chat

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.add
import kotlinx.serialization.json.buildJsonArray
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.FeedItem
import persona.core.contracts.FeedResponse
import persona.core.contracts.Interaction
import persona.core.contracts.Item
import persona.core.contracts.Machine
import persona.core.contracts.Resolution
import persona.core.contracts.ServerEvent
import persona.core.contracts.Session
import persona.core.contracts.SessionKey
import persona.core.contracts.TaskPlan
import persona.core.contracts.Todo
import persona.core.contracts.api.ChatRepository
import persona.core.contracts.api.ConnectionState
import persona.core.contracts.api.HubConnection
import persona.core.contracts.api.ChatSnapshot
import persona.core.contracts.api.InteractionCenter
import persona.core.contracts.api.MessageSender
import persona.core.contracts.api.OutboxMessage
import persona.core.contracts.api.SessionsRepository
import persona.core.contracts.api.TasksApi
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaTheme
import persona.core.domain.tasks.TasksStore
import java.io.File

/**
 * The chat's model operating a Mac (#4339 C9 and C18): an Agent's chat whose agent drives Calculator through its own
 * Cua Driver tools, and Persona's conversation running computer_run on the same Mac, in every style, light and dark.
 * On Glass the pill says "Controlling MacBook · Click AC → 6 → × → 7 → =" over the Agent chat's composer and under the
 * entries in Persona's; Brief and Board draw no pill. The items carry no times, so nothing on the page moves between runs.
 * PNGs in `build/computer-pill-shots/` (or `-Dpersona.shots=<dir>`).
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h900dp-xhdpi")
class ChatComputerPillShotsTest {
    @get:Rule val rule = createComposeRule()

    private fun out(): File = File(System.getProperty("persona.shots") ?: "build/computer-pill-shots").also { it.mkdirs() }

    private val mac = Machine("macbook", "MacBook", online = true)
    private val hub = Machine("hub", "Hub", online = true)
    private val agent = SessionKey(mac.id, "calc")
    private val persona = SessionKey(hub.id, "main")
    private val asked = "Work out 6 × 7 on the Mac's Calculator and tell me what it shows."
    private val opening = "Opening Calculator and typing it in."

    private fun steps() = buildJsonArray {
        listOf("All Clear", "6", "Multiply", "7", "Equals").forEach { name ->
            add(buildJsonObject { put("click", buildJsonObject { put("app", "Calculator"); put("role", "button"); put("name", name) }) })
        }
    }

    private fun use(seq: Int, name: String, input: JsonObject) =
        Item("u$seq", seq, role = "assistant", kind = "tool_use", toolUseId = "t$seq", toolName = name, toolInput = input)

    private fun result(seq: Int, of: Int) = Item("r$seq", seq, role = "user", kind = "tool_result", toolUseId = "t$of", text = "ok")

    private fun said(seq: Int, role: String, text: String) = Item("m$seq", seq, role = role, kind = "text", text = text)

    /** The Agent's own Cua Driver: Calculator opened, the sum being clicked in (no result yet). */
    private val agentItems = listOf(
        said(1, "user", asked), said(2, "assistant", opening),
        use(3, "Bash", buildJsonObject { put("command", "open -a Calculator") }), result(4, 3),
        use(5, "mcp__cua-driver__launch_app", buildJsonObject { put("name", "Calculator") }), result(6, 5),
        use(7, "mcp__cua-driver__run_steps", buildJsonObject { put("steps", steps()) }),
    )

    /** Persona's computer tools on the MacBook: it looked at Calculator, and is clicking the sum in. */
    private val personaItems = listOf(
        said(1, "user", asked), said(2, "assistant", opening),
        use(3, "mcp__persona__computer_state", buildJsonObject { put("machine", "macbook"); put("app", "Calculator") }), result(4, 3),
        use(5, "mcp__persona__computer_run", buildJsonObject { put("machine", "macbook"); put("steps", steps()) }),
    )

    private fun chats(items: List<Item>) = object : ChatRepository {
        override fun observe(session: SessionKey) = MutableStateFlow(ChatSnapshot(items = items, loading = false))
        override suspend fun loadOlder(session: SessionKey) = Unit
        override suspend fun expandFold(session: SessionKey, fromSeq: Int, toSeq: Int): List<Item> = emptyList()
    }

    private fun sessions(vararg rows: Session) = object : SessionsRepository {
        override val machines = MutableStateFlow(listOf(hub, mac))
        override val sessions = MutableStateFlow(rows.toList())
        override suspend fun refresh() = Unit
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

    private val tasksApi = object : TasksApi {
        override suspend fun todos() = emptyList<Todo>()
        override suspend fun feed(conversation: String, since: String?) = FeedResponse(emptyList())
        override suspend fun startPlan(id: String, remember: Boolean): TaskPlan = error("not used")
        override suspend fun dismissPlan(id: String): TaskPlan = error("not used")
        override suspend fun retryTask(todoId: String): Todo = error("not used")
        override suspend fun removeTask(todoId: String) = Unit
        override suspend fun route(conversation: String, todoId: String, text: String): FeedItem = error("not used")
        override suspend fun today(conversation: String): FeedItem = error("not used")
        override suspend fun mainConversation() = "main"
        override suspend fun inboxReply(itemId: String, text: String, card: String?) = Unit
    }
    private val connection = object : HubConnection {
        override val state = MutableStateFlow(ConnectionState.WaitingForNetwork)
        override val events = MutableSharedFlow<ServerEvent>()
        override fun reconnect() = Unit
        override fun send(message: JsonObject) = true
    }

    private fun animationsOff() {
        val context = androidx.test.core.app.ApplicationProvider.getApplicationContext<android.content.Context>()
        android.provider.Settings.Global.putFloat(context.contentResolver, android.provider.Settings.Global.ANIMATOR_DURATION_SCALE, 0f)
    }

    private fun name(style: AppStyle, dark: Boolean) = "${style.name.lowercase()}-${if (dark) "dark" else "light"}"

    private fun shot(name: String) = File(out(), "$name.png").outputStream().use {
        rule.onNodeWithTag("page").captureToImage().asAndroidBitmap().compress(Bitmap.CompressFormat.PNG, 100, it)
    }

    private fun settle() {
        rule.waitUntil(20_000) { rule.onAllNodesWithText(opening, substring = true).fetchSemanticsNodes().isNotEmpty() }
        rule.waitForIdle()
    }

    private fun agentChat(style: AppStyle, dark: Boolean) {
        animationsOff()
        val row = Session(machineId = agent.machineId, sessionId = agent.sessionId, title = "Work out 6 × 7", status = "working",
            state = "working", agent = "claude", paneId = "p1", cwd = "/Users/me/calc", spaceName = "calc", spaceId = "w1", slotName = "calc")
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                Box(Modifier.fillMaxSize().background(PersonaTheme.palette.background).testTag("page")) {
                    ChatScreen(agent, chats(agentItems), sender, interactions, sessions(row), onBack = {})
                }
            }
        }
        settle()
        shot("computer-agent-${name(style, dark)}")
    }

    private fun personaChat(style: AppStyle, dark: Boolean) {
        animationsOff()
        val row = Session(persona.machineId, persona.sessionId, title = "Persona", assistant = true, status = "working", state = "working")
        val store = TasksStore(tasksApi, connection, CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)).also { it.reloadTodos() }
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                Box(Modifier.fillMaxSize().background(PersonaTheme.palette.background).testTag("page")) {
                    ChatScreen(persona, chats(personaItems), sender, interactions, sessions(row), onBack = {},
                        chrome = ChatChromeBindings(tasks = store), renderStyle = ChatRenderStyle.Assistant, showTopBar = false)
                }
            }
        }
        settle()
        shot("computer-persona-${name(style, dark)}")
    }

    @Test fun agentGlassLight() = agentChat(AppStyle.Glass, dark = false)
    @Test fun agentGlassDark() = agentChat(AppStyle.Glass, dark = true)
    @Test fun agentBriefLight() = agentChat(AppStyle.Brief, dark = false)
    @Test fun agentBriefDark() = agentChat(AppStyle.Brief, dark = true)
    @Test fun agentBoardLight() = agentChat(AppStyle.Board, dark = false)
    @Test fun agentBoardDark() = agentChat(AppStyle.Board, dark = true)
    @Test fun personaGlassLight() = personaChat(AppStyle.Glass, dark = false)
    @Test fun personaGlassDark() = personaChat(AppStyle.Glass, dark = true)
    @Test fun personaBriefLight() = personaChat(AppStyle.Brief, dark = false)
    @Test fun personaBriefDark() = personaChat(AppStyle.Brief, dark = true)
    @Test fun personaBoardLight() = personaChat(AppStyle.Board, dark = false)
    @Test fun personaBoardDark() = personaChat(AppStyle.Board, dark = true)
}
