package persona.feature.assistant

import android.content.res.Configuration
import android.graphics.Bitmap
import androidx.activity.ComponentActivity
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.unit.dp
import androidx.navigation3.runtime.NavKey
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.MutableStateFlow
import org.junit.After
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.Machine
import persona.core.contracts.Session
import persona.core.contracts.SessionKey
import persona.core.contracts.api.*
import persona.core.designsystem.*
import persona.core.designsystem.icons.PhIcons
import persona.core.navigation.AppTab
import persona.core.navigation.Navigator
import java.io.File

/**
 * New chat's composer with "@" typed (#4339 C16), in every style, light and dark, inside the shell's bar: at rest (with
 * what needs the user), the @ list of sessions, a typed home folder, and a picked session's quote card. The same steps on `main` (without the
 * page's `mentionSessions` and `mentionFiles`) draw the before. Written as PNGs to `build/new-chat-mention-shots/` (or
 * `-Dpersona.shots=<dir>`).
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h900dp-xhdpi")
class NewChatMentionsShotsTest {
    @get:Rule val rule = createAndroidComposeRule<ComponentActivity>()
    private val main = SessionKey("mac", "main")
    private val navigator = object : Navigator {
        override fun navigate(key: NavKey) = Unit
        override fun back() = Unit
        override fun selectTab(tab: AppTab) = Unit
    }
    private val sessions = MutableStateFlow(listOf(
        Session(machineId = "mbp", sessionId = "s1", title = "Fix the login page", cwd = "/Users/me/code/weather-app",
            updatedAt = "2026-11-12T09:40:00Z"),
        Session(machineId = "studio", sessionId = "s2", title = "Photo sync", cwd = "/Users/me/code/photos", agent = "codex",
            updatedAt = "2026-11-12T10:05:00Z", lastText = "Synced 120 photos."),
        Session(machineId = "mbp", sessionId = "s3", title = "Release notes for 2.4", cwd = "/Users/me/code/site",
            updatedAt = "2026-11-11T17:20:00Z"),
        Session(machineId = "mac", sessionId = "main", title = "Main chat", assistant = true, updatedAt = "2026-11-12T11:00:00Z"),
    ))

    @Before fun setup() = AssistantManagerChoice.choose("hub", null, "assistant")
    @After fun cleanup() = AssistantManagerChoice.reset()

    private class Hub(main: SessionKey) : AssistantRepository, MessageSender, MainConversationSender {
        override val assistant = MutableStateFlow(AssistantSnapshot(mainSession = main, conversations = listOf(
            Session(machineId = "mac", sessionId = "main", title = "Main chat", assistant = true),
        )))
        override val outbox = MutableStateFlow(emptyList<OutboxMessage>())
        override suspend fun refresh() = Unit
        override suspend fun sendToMain(text: String, clientMessageId: String, draft: DraftSnapshot?) = Unit
        override suspend fun send(session: SessionKey, text: String, clientMessageId: String, clearDraft: String?) = Unit
        suspend fun start() = SessionKey("mac", "new")
    }

    /** The assistant's computer: its home folder. */
    private class Home : SessionFiles {
        override suspend fun directory(machineId: String, cwd: String, path: String) = SessionDirectory(path, listOf(
            SessionFileEntry("Documents", dir = true), SessionFileEntry("code", dir = true), SessionFileEntry("Downloads", dir = true),
            SessionFileEntry(".zshrc"), SessionFileEntry("notes.md"), SessionFileEntry("trip-plan.pdf"),
        ))
        override suspend fun file(machineId: String, cwd: String, path: String) = throw UnsupportedOperationException()
        override suspend fun status(machineId: String, cwd: String) = throw UnsupportedOperationException()
        override suspend fun diff(machineId: String, cwd: String, path: String, staged: Boolean) = throw UnsupportedOperationException()
        override suspend fun log(machineId: String, cwd: String, skip: Int, limit: Int) = throw UnsupportedOperationException()
        override suspend fun show(machineId: String, cwd: String, hash: String) = throw UnsupportedOperationException()
    }

    @Test fun newChatsComposerWithAt() {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        var round by mutableIntStateOf(0)
        val hub = Hub(main)
        val sidebar = AssistantSidebarHost()
        val home = Home()
        // What needs the user: on Glass over the composer at rest, stepping aside while the @ list is open.
        val needs = NeedsYou(listOf(
            NeedsYouLine.Agent(SessionKey("mbp", "agent"), "r1", "plan", null, who = "weather-app", what = "Plan ready"),
            NeedsYouLine.Inbox("i1", lark = false, itemKind = "mail", who = "Alex", what = "Re: Q4 budget"),
        ))
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                val configuration = Configuration(LocalConfiguration.current).apply {
                    uiMode = (uiMode and Configuration.UI_MODE_NIGHT_MASK.inv()) or
                        if (dark) Configuration.UI_MODE_NIGHT_YES else Configuration.UI_MODE_NIGHT_NO
                }
                // The shell's bar and its one sidebar, as the app shows the page (Root.kt).
                val chrome = HomeChrome(listOf(NavItem("Persona", "Assistant", PhIcons.Sparkle), NavItem("Agents", "Agents", PhIcons.Terminal)),
                    "Persona", {}, { JPersonaButton(PersonaState.Idle, "Chat history", {}) })
                CompositionLocalProvider(LocalConfiguration provides configuration, LocalManagerRoster provides ManagerRoster(listOf(
                    HubManager("assistant", "Helper", online = true, isDefault = true),
                ), "assistant"), LocalHomeChrome provides chrome, LocalAssistantSidebarHost provides sidebar) {
                    Box(Modifier.testTag("shot").width(400.dp).height(900.dp).background(PersonaTheme.palette.background)) {
                        key(round) {
                            AssistantScreen(navigator, hub, hub, hub::start, composerService = null, mainConversationSender = hub,
                                requestedSession = main, needsYou = needs, shellNeedsYou = needs.lines, onOpenNeed = {}, onOpenInbox = {},
                                machines = listOf(Machine("mbp", name = "MacBook Pro", online = true)),
                                phoneToolsAvailable = true, chatContent = { Box(Modifier.fillMaxSize()) },
                                mentionSessions = sessions, mentionFiles = home)
                        }
                    }
                }
            }
        }
        fun capture(name: String) {
            rule.waitForIdle()
            val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
            val out = File(System.getProperty("persona.shots") ?: "build/new-chat-mention-shots").also { it.mkdirs() }
            File(out, "$name.png").outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
        }
        fun type(text: String) {
            rule.onNode(hasSetTextAction()).performTextReplacement(text)
            rule.mainClock.advanceTimeBy(500)
            rule.waitForIdle()
        }
        for (s in AppStyle.entries) for (d in listOf(false, true)) {
            style = s; dark = d; round++
            rule.waitForIdle()
            val name = "${s.name.lowercase()}-${if (d) "dark" else "light"}"
            rule.onNodeWithContentDescription("New chat").performClick()
            capture("$name-1-rest")
            rule.onNode(hasSetTextAction()).performClick()
            type("@")
            capture("$name-2-at")
            type("@~/")
            capture("$name-3-folder")
            type("@photo")
            // On main there is no list to pick from.
            if (rule.onAllNodesWithText("Photo sync").fetchSemanticsNodes().isNotEmpty()) rule.onNodeWithText("Photo sync").performClick()
            type("Compare this with the last sync")
            capture("$name-4-quoted")
        }
    }
}
