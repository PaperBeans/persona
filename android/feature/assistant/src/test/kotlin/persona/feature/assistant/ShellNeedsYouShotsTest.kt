package persona.feature.assistant

import android.content.res.Configuration
import android.graphics.Bitmap
import androidx.activity.ComponentActivity
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.width
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
import org.junit.Assert.assertEquals
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.Machine
import persona.core.contracts.SessionKey
import persona.core.contracts.api.*
import persona.core.designsystem.*
import persona.core.designsystem.icons.PhIcons
import persona.core.navigation.AppTab
import persona.core.navigation.Navigator
import java.io.File

/**
 * Needs you over the blank page's composer on Glass (#4339 C17), light and dark, inside the shell's bar: one line; three
 * lines; three and the rest's line on a tall page; one and the rest's line on a short one. Written as PNGs to
 * `build/needs-you-shots/` (or `-Dpersona.shots=<dir>`).
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h1000dp-xhdpi")
class ShellNeedsYouShotsTest {
    @get:Rule val rule = createAndroidComposeRule<ComponentActivity>()
    private val navigator = object : Navigator {
        override fun navigate(key: NavKey) = Unit
        override fun back() = Unit
        override fun selectTab(tab: AppTab) = Unit
    }

    @Before fun setup() = AssistantManagerChoice.choose("hub", null, "assistant")
    @After fun cleanup() = AssistantManagerChoice.reset()

    private class Hub : AssistantRepository, MessageSender, MainConversationSender {
        override val assistant = MutableStateFlow(AssistantSnapshot(mainSession = SessionKey("mac", "main")))
        override val outbox = MutableStateFlow(emptyList<OutboxMessage>())
        override suspend fun refresh() = Unit
        override suspend fun sendToMain(text: String, clientMessageId: String, draft: DraftSnapshot?) = Unit
        override suspend fun send(session: SessionKey, text: String, clientMessageId: String, clearDraft: String?) = Unit
        suspend fun start() = SessionKey("mac", "new")
    }

    private val plan = NeedsYouLine.Agent(SessionKey("mac", "weather"), "r1", "plan", null, who = "weather-app", what = "Plan awaiting approval",
        row = "agent:mac/weather")
    private val question = NeedsYouLine.Agent(SessionKey("mac", "blog"), "r2", "question", null, who = "Blog migration",
        what = "Which post should I move first?", row = "agent:mac/blog")
    private val shell = NeedsYouLine.Agent(SessionKey("mac", "deploy"), "r3", "permission", "Bash", who = "Deploy",
        what = "Waiting for your approval: npm run deploy", row = "agent:mac/deploy")
    private val terminal = NeedsYouLine.Agent(SessionKey("mac", "release"), "", "screen", null, who = "Release notes", what = "",
        row = "agent:mac/release", terminal = true)
    private val mail = NeedsYouLine.Inbox("i1", lark = false, itemKind = "mail", who = "Alex", what = "Re: Q4 budget")
    private val lark = NeedsYouLine.Inbox("i2", lark = true, itemKind = "chat", who = "Design review", what = "Can you look at the new icons?")

    @Test fun overTheComposer() {
        var dark by mutableStateOf(false)
        var lines by mutableStateOf(listOf<NeedsYouLine>(plan))
        var tall by mutableStateOf(true)
        var round by mutableIntStateOf(0)
        val hub = Hub()
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = AppStyle.Glass) {
                val configuration = Configuration(LocalConfiguration.current).apply {
                    uiMode = (uiMode and Configuration.UI_MODE_NIGHT_MASK.inv()) or
                        if (dark) Configuration.UI_MODE_NIGHT_YES else Configuration.UI_MODE_NIGHT_NO
                }
                val chrome = HomeChrome(listOf(NavItem("Persona", "Assistant", PhIcons.Sparkle), NavItem("Agents", "Agents", PhIcons.Terminal)),
                    "Persona", {}, { JPersonaButton(PersonaState.Idle, "Chat history", {}) })
                CompositionLocalProvider(LocalConfiguration provides configuration, LocalHomeChrome provides chrome) {
                    Box(Modifier.testTag("shot").width(400.dp).height(if (tall) 1000.dp else 760.dp).background(PersonaTheme.palette.background)) {
                        key(round) {
                            AssistantScreen(navigator, hub, hub, hub::start, composerService = null, mainConversationSender = hub,
                                shellNeedsYou = lines, onOpenNeed = {}, onOpenInbox = {},
                                machines = listOf(Machine("mbp", name = "MacBook Pro", online = true)), phoneToolsAvailable = true,
                                chatContent = {})
                        }
                    }
                }
            }
        }
        fun capture(name: String, expected: Int) {
            rule.waitForIdle()
            // From the main conversation, New chat's page.
            rule.onNodeWithContentDescription("New chat").performClick()
            rule.waitForIdle()
            val shown = rule.onAllNodesWithTag("shell-needs-agent").fetchSemanticsNodes().size +
                rule.onAllNodesWithTag("shell-needs-inbox").fetchSemanticsNodes().size
            assertEquals(name, expected, shown)
            val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
            val out = File(System.getProperty("persona.shots") ?: "build/needs-you-shots").also { it.mkdirs() }
            File(out, "$name.png").outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
        }
        for (d in listOf(false, true)) {
            dark = d
            val mode = if (d) "dark" else "light"
            lines = listOf(plan); tall = true; round++
            capture("glass-$mode-1-one-line", 1)
            lines = listOf(plan, terminal, mail); round++
            capture("glass-$mode-2-three-lines", 3)
            lines = listOf(plan, question, shell, terminal, mail, lark); round++
            capture("glass-$mode-3-three-and-more", 3)
            tall = false; round++
            capture("glass-$mode-4-short-page", 1)
        }
    }
}
