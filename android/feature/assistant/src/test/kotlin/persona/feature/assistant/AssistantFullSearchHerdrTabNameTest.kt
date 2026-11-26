package persona.feature.assistant

import androidx.activity.ComponentActivity
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.Session
import persona.core.designsystem.PersonaTheme

@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en")
class AssistantFullSearchHerdrTabNameTest {
    @get:Rule val rule = createAndroidComposeRule<ComponentActivity>()

    @Test fun agentSearchShowsHerdrTabNameAsMainTitleAndCliTitleInSubtitleWhenEnabled() {
        val agent = Session(
            machineId = "mac",
            sessionId = "export",
            title = "CLI session name",
            cwd = "/work/export",
            status = "idle",
            state = "idle",
            spaceKind = "herdr",
            slotId = "w1:t1",
            slotName = "Export work",
            paneId = "w1:p1",
        )
        var hubs by mutableStateOf<Set<String?>>(emptySet())
        rule.setContent {
            PersonaTheme(reduceMotion = true) {
                AssistantFullSearch(
                    chats = emptyList(),
                    known = emptyList(),
                    searchOn = false,
                    management = null,
                    hubKey = "fixture",
                    words = ConversationWords("Chat", "Hand to Persona", "Hand to Persona: %1\$s"),
                    onOpen = {},
                    onDismiss = {},
                    initialQuery = "Export",
                    overlay = false,
                    agents = listOf(agent),
                    agentMachines = mapOf("mac" to "MacBook"),
                    herdrTabNameHubs = hubs,
                    agentsFirst = true,
                    onOpenAgent = {},
                )
            }
        }
        rule.onNodeWithText("CLI session name").assertExists()
        rule.onNodeWithText("MacBook").assertExists()

        rule.runOnIdle { hubs = setOf(null) }
        rule.waitForIdle()
        rule.onNodeWithText("Export work").assertExists()
        rule.onNodeWithText("CLI session name · MacBook").assertExists()
    }
}
