package persona.feature.assistant

import android.content.res.Configuration
import android.graphics.Bitmap
import androidx.activity.ComponentActivity
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
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
import persona.feature.chat.ChatComposer
import persona.feature.chat.rememberChatComposerState
import java.io.File

/**
 * Where Back from a conversation lands and New chat's page (#4339 C11), in every style, light and dark, and the Assistant
 * tab's menu on Glass, inside the shell's bar: the same steps on `main` and on the branch draw the before and after. The conversation itself is a stand-in (the
 * shared chat page needs the app's services); the page around it is the real one. Written as PNGs to
 * `build/no-home-shots/` (or `-Dpersona.shots=<dir>`).
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h900dp-xhdpi")
class NoHomePageShotsTest {
    @get:Rule val rule = createAndroidComposeRule<ComponentActivity>()
    private val main = SessionKey("mac", "main")
    private val side = SessionKey("mac", "side")
    private val navigator = object : Navigator {
        override fun navigate(key: NavKey) = Unit
        override fun back() = Unit
        override fun selectTab(tab: AppTab) = Unit
    }
    private val words = mapOf(
        "main" to Triple("Main chat", "What's on my calendar today?", "Two meetings: design review at 10:00 and a 1:1 at 15:30."),
        "side" to Triple("Trip plan", "Plan a weekend in the mountains", "Here is a two-day plan with a cabin near the trailhead."),
    )

    @Before fun setup() = AssistantManagerChoice.choose("hub", null, "assistant")
    @After fun cleanup() = AssistantManagerChoice.reset()

    private class Hub(main: SessionKey) : AssistantRepository, MessageSender, MainConversationSender {
        override val assistant = MutableStateFlow(AssistantSnapshot(mainSession = main, conversations = listOf(
            Session(machineId = "mac", sessionId = "main", title = "Main chat", assistant = true),
            Session(machineId = "mac", sessionId = "side", title = "Trip plan", assistant = true),
        )))
        override val outbox = MutableStateFlow(emptyList<OutboxMessage>())
        override suspend fun refresh() = Unit
        override suspend fun sendToMain(text: String, clientMessageId: String, draft: DraftSnapshot?) = Unit
        override suspend fun send(session: SessionKey, text: String, clientMessageId: String, clearDraft: String?) = Unit
        suspend fun start() = SessionKey("mac", "new")
    }

    @Test fun backNewChatAndTheMenu() {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        var round by mutableIntStateOf(0)
        val hub = Hub(main)
        val sidebar = AssistantSidebarHost()
        val needs = NeedsYou(listOf(
            NeedsYouLine.Agent(SessionKey("mac", "agent"), "r1", "plan", null, who = "weather-app", what = "Plan ready"),
            NeedsYouLine.Inbox("i1", lark = false, itemKind = "mail", who = "Alex", what = "Re: Q4 budget"),
        ), more = 1)
        // Glass draws them over the composer (#4339 C17), all of them, as many as fit.
        val shellNeeds = needs.lines + NeedsYouLine.Inbox("i2", lark = false, itemKind = "mail", who = "Sam", what = "Lunch on Friday?")
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
                                requestedSession = side, needsYou = needs, shellNeedsYou = shellNeeds, onOpenNeed = {}, onOpenInbox = {},
                                machines = listOf(Machine("mbp", name = "MacBook Pro", online = true)), phoneToolsAvailable = true,
                                chatContent = { key -> StandInChat(key) })
                        }
                    }
                }
            }
        }
        fun capture(name: String, dialog: Boolean = false) {
            rule.waitForIdle()
            val node = if (dialog) rule.onNode(isDialog()) else rule.onNodeWithTag("shot")
            val bitmap = node.captureToImage().asAndroidBitmap()
            val out = File(System.getProperty("persona.shots") ?: "build/no-home-shots").also { it.mkdirs() }
            File(out, "$name.png").outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
        }
        fun back() { rule.runOnIdle { rule.activity.onBackPressedDispatcher.onBackPressed() }; rule.waitForIdle() }
        for (s in AppStyle.entries) for (d in listOf(false, true)) {
            style = s; dark = d; round++
            rule.waitForIdle()
            val name = "${s.name.lowercase()}-${if (d) "dark" else "light"}"
            rule.onNodeWithText("Plan a weekend in the mountains").assertExists()
            capture("$name-1-conversation")
            back()
            capture("$name-2-back")
            rule.onNodeWithContentDescription("New chat").performClick()
            capture("$name-3-new-chat")
            // The menu opens from the selected tab on Glass (Brief and Board's switcher has none).
            if (s != AppStyle.Glass) continue
            round++
            rule.waitForIdle()
            rule.onNodeWithText("Assistant").performClick()
            capture("$name-4-menu", dialog = true)
            back()
        }
    }

    /** The conversation as the shared chat page draws it, roughly: a question, an answer, the composer under them. */
    @Composable
    private fun StandInChat(key: SessionKey) {
        val (title, asked, answer) = words.getValue(key.sessionId)
        val insets = LocalChromeInsets.current
        Column(Modifier.fillMaxSize().padding(top = insets.top, bottom = insets.bottom)) {
            Column(Modifier.weight(1f).fillMaxWidth().padding(horizontal = Tokens.Space.l, vertical = Tokens.Space.l),
                verticalArrangement = Arrangement.spacedBy(Tokens.Space.m)) {
                Text(title, Modifier.align(Alignment.CenterHorizontally), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink3)
                Spacer(Modifier.weight(1f))
                UserBubble(asked)
                Text(answer, style = PersonaTheme.type.body, color = PersonaTheme.palette.ink)
            }
            ChatComposer(key, rememberChatComposerState(), working = false, sending = false, canClear = false, service = null,
                onSend = {}, onCommand = {}, onNotice = {}, placeholder = "Ask Persona…")
        }
    }
}
