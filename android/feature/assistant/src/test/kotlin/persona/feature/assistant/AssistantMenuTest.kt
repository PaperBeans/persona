package persona.feature.assistant

import android.content.ClipboardManager
import androidx.activity.ComponentActivity
import androidx.compose.material3.Text
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.navigation3.runtime.NavKey
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.MutableStateFlow
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.Machine
import persona.core.contracts.Session
import persona.core.contracts.SessionKey
import persona.core.contracts.api.*
import persona.core.designsystem.*
import persona.core.navigation.AppTab
import persona.core.navigation.Navigator
import persona.feature.assistant.AssistantMenuItem.*
import java.io.File

/**
 * The title menu's items in iOS's order (#4339 C18, iOS `AssistantTopBar.menuItems`): Connection details while the hub is
 * out of reach, Knowledge, Details, Back to main chat, the copies, the main conversation's actions, Session info and Interrupt;
 * and what Copy directory and Copy info put on the clipboard (iOS `AssistantCopyText`).
 */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en-w400dp-h900dp-xhdpi")
class AssistantMenuTest {
    @get:Rule val rule = createAndroidComposeRule<ComponentActivity>()
    private val main = SessionKey("mac", "main")
    private val side = SessionKey("mac", "side")
    private val navigator = object : Navigator {
        override fun navigate(key: NavKey) = Unit
        override fun back() = Unit
        override fun selectTab(tab: AppTab) = Unit
    }

    @Before fun setup() = AssistantManagerChoice.choose("hub", null, "assistant")
    @After fun cleanup() = AssistantManagerChoice.reset()

    @Test fun theItemsComeInIosOrderAndSections() {
        assertEquals(listOf(listOf(ConnectionDetails, Knowledge, Details, Voice, BackToMain),
            listOf(CopyDirectory, CopyInfo, CopySessionId, CopyDiagnostics), listOf(SessionInfo, Interrupt)),
            assistantMenu(offline = true, voice = true, main = main, active = side, compact = false, newMain = false, working = true))
        // The main conversation: no way back to it, its own actions, nothing to interrupt while it rests.
        assertEquals(listOf(listOf(Knowledge, Details), listOf(CopyDirectory, CopyInfo, CopySessionId, CopyDiagnostics),
            listOf(Compact, NewMain), listOf(SessionInfo)),
            assistantMenu(offline = false, voice = false, main = main, active = main, compact = true, newMain = true, working = false))
        // A blank page copies what a new conversation would use, and has no session to show or stop.
        assertEquals(listOf(listOf(Knowledge, Details, BackToMain), listOf(CopyDirectory, CopyInfo)),
            assistantMenu(offline = false, voice = false, main = main, active = null, compact = false, newMain = false, working = true))
    }

    @Test fun copyInfoSaysWhichConversationWhereAndHowToGoOn() {
        val res = ApplicationProvider.getApplicationContext<android.content.Context>().resources
        assertEquals("""
            Persona assistant chat Main chat “Trip plan”
            Session ID: s1
            Machine: MacBook (mac)
            Working directory: ~/work
            Launcher: claude · Model: opus
            To continue on that machine: cd ~/work && claude --resume s1
        """.trimIndent(), assistantCopyInfo(res, "Trip plan", main = true, sessionId = "s1", machine = "MacBook", machineId = "mac",
            cwd = "~/work", launcher = "claude", model = "opus"))
        assertEquals("""
            Persona assistant (Claude on the hub)
            Machine: mac
            Working directory: ~/.persona/assistant
            Launcher: claude
        """.trimIndent(), assistantCopyInfo(res, null, main = false, sessionId = null, machine = "mac", machineId = "mac",
            cwd = AssistantDefaultCwd, launcher = "claude", model = null))
        // A side conversation keeps its own launcher, the main one follows the settings; a provider path shows its model.
        val config = AssistantConfig(machineId = "mac", launcher = "glm", cwd = "~/assistant")
        val session = Session(machineId = "mac", sessionId = "side", title = "Claude Code", cwd = "", launcher = "claude", model = "anthropic/opus")
        val copy = AssistantCopyText(session, side, main = false, config = config, assistantMachine = "", machines = emptyList())
        assertEquals("~/assistant", copy.cwd)
        assertEquals("claude", copy.launcher)
        assertEquals("glm", AssistantCopyText(session, main, main = true, config = config, assistantMachine = "", machines = emptyList()).launcher)
        assertEquals("""
            Persona assistant chat
            Session ID: side
            Machine: mac
            Working directory: ~/assistant
            Launcher: claude · Model: opus
            To continue on that machine: cd ~/assistant && claude --resume side
        """.trimIndent(), copy.info(res))
    }

    private inner class Hub : AssistantRepository, MessageSender, MainConversationSender {
        override val assistant = MutableStateFlow(AssistantSnapshot(mainSession = main, conversations = listOf(
            Session(machineId = "mac", sessionId = "main", title = "Main", assistant = true),
            Session(machineId = "mac", sessionId = "side", title = "Trip plan", assistant = true, status = "working", cwd = "~/trips"),
        )))
        override val outbox = MutableStateFlow(emptyList<OutboxMessage>())
        override suspend fun refresh() = Unit
        override suspend fun sendToMain(text: String, clientMessageId: String, draft: DraftSnapshot?) = Unit
        override suspend fun send(session: SessionKey, text: String, clientMessageId: String, clearDraft: String?) = Unit
        suspend fun start() = SessionKey("mac", "new")
    }

    private class Composer : ChatComposerService {
        val interrupted = mutableListOf<SessionKey>()
        override val deliveries = MutableStateFlow(emptyMap<String, ComposerDelivery>())
        override suspend fun upload(session: SessionKey, name: String, file: File) = error("not used")
        override suspend fun interrupt(session: SessionKey) { interrupted += session }
        override suspend fun retryRefused(clientMessageId: String, clearDraft: Boolean) = error("not used")
    }

    @Test fun theMenuCopiesInterruptsAndSaysTheLinkIsDown() {
        val hub = Hub()
        val composer = Composer()
        rule.setContent {
            PersonaTheme(reduceMotion = true) {
                CompositionLocalProvider(LocalManagerRoster provides ManagerRoster(listOf(
                    HubManager("assistant", "Helper", online = true, isDefault = true),
                ), "assistant")) {
                    AssistantScreen(navigator, hub, hub, hub::start, composerService = composer, mainConversationSender = hub,
                        requestedSession = side, connection = ConnectionState.WaitingForNetwork,
                        machines = listOf(Machine("mac", name = "MacBook", online = true)),
                        chatContent = { Text("chat:${it.sessionId}") })
                }
            }
        }
        rule.waitForIdle()
        fun menu() { rule.onNodeWithText("Persona").performClick(); rule.waitForIdle() }
        menu()
        for (item in listOf("Connection details", "Knowledge", "Details", "Back to main chat", "Copy directory", "Copy info",
            "Copy session ID", "Copy diagnostics", "Session info", "Interrupt")) rule.onNodeWithText(item).assertExists()
        rule.onAllNodesWithText("Assistant settings").assertCountEquals(0)
        rule.onNodeWithText("Copy directory").performClick()
        rule.waitForIdle()
        val clipboard = rule.activity.getSystemService(ClipboardManager::class.java)
        assertEquals("~/trips", clipboard.primaryClip?.getItemAt(0)?.text?.toString())
        menu()
        rule.onNodeWithText("Copy info").performClick()
        rule.waitForIdle()
        val info = clipboard.primaryClip?.getItemAt(0)?.text?.toString().orEmpty()
        assertEquals("Persona assistant chat “Trip plan”", info.lines().first())
        assertEquals("To continue on that machine: cd ~/trips && claude --resume side", info.lines().last())
        menu()
        rule.onNodeWithText("Interrupt").performClick()
        rule.waitUntil(5_000) { composer.interrupted.isNotEmpty() }
        assertEquals(listOf(side), composer.interrupted)
        menu()
        rule.onNodeWithText("Connection details").performClick()
        rule.waitForIdle()
        rule.onNodeWithTag("connection-details").assertExists()
    }
}
