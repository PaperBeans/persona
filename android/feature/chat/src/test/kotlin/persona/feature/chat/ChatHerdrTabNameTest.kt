package persona.feature.chat

import androidx.activity.ComponentActivity
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.test.assertIsEnabled
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextClearance
import androidx.compose.ui.test.performTextInput
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.Machine
import persona.core.contracts.Session
import persona.core.contracts.SessionCapabilities
import persona.core.contracts.api.ConnectionState
import persona.core.designsystem.PersonaTheme
import persona.core.domain.sort.SessionRename
import persona.core.domain.sort.SessionRenamer

@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en")
class ChatHerdrTabNameTest {
    @get:Rule val rule = createAndroidComposeRule<ComponentActivity>()

    private val herdrSession = Session(
        machineId = "mac",
        sessionId = "export",
        title = "CLI session name",
        cwd = "/work/export",
        status = "idle",
        state = "idle",
        agent = "claude",
        spaceKind = "herdr",
        spaceId = "w1",
        spaceName = "persona",
        slotId = "w1:t1",
        slotName = "Export work",
        paneId = "w1:p1",
    )

    @Test fun chatPlaceAndRenameNoteFollowPreferHerdrTabName() {
        assertEquals("MacBook · persona › Export work", chatPlace(herdrSession, "MacBook", preferHerdrTabName = false))
        assertEquals("CLI session name · MacBook · persona", chatPlace(herdrSession, "MacBook", preferHerdrTabName = true))
        assertEquals("CLI session name · persona", chatPlace(herdrSession, "MacBook", short = true, preferHerdrTabName = true))
        assertEquals("MacBook · persona", chatPlace(herdrSession.copy(title = "Export work"), "MacBook", preferHerdrTabName = true))

        assertEquals(R.string.chat_chrome_rename_text_unified, chatRenameNote(herdrSession, unified = true))
        assertEquals(R.string.chat_chrome_rename_text, chatRenameNote(herdrSession, unified = false))
        assertNull(chatRenameNote(herdrSession.copy(sourceKind = "runtime", capabilities = SessionCapabilities(read = true, send = true, rename = true)), unified = true))
    }

    @Test fun chatHeaderAndRenameDialogSwitchBetweenCliAndHerdrTabNaming() {
        val calls = mutableListOf<SessionRename>()
        val renamer = object : SessionRenamer {
            override fun project(source: Flow<List<Session>>) = source
            override suspend fun rename(session: Session, title: String?, tabName: String?): Boolean {
                calls += SessionRename(title = title, tabName = tabName)
                return true
            }
        }
        var preferTab by mutableStateOf(false)
        var current by mutableStateOf(herdrSession)
        val machine = Machine("mac", name = "MacBook", online = true)
        rule.setContent {
            val scope = rememberCoroutineScope()
            val chrome = ChatChromeState(rule.activity.resources, null, scope, {}, {}, renamer)
            PersonaTheme(reduceMotion = true) {
                ChatHeader(
                    session = current,
                    machine = machine,
                    sessions = listOf(current),
                    state = chrome,
                    connection = ConnectionState.Online,
                    readOnly = false,
                    onBack = {},
                    onOpenSession = null,
                    onConnectionDetails = null,
                    onOverlay = {},
                    preferHerdrTabName = preferTab,
                )
            }
        }

        // Default off: main title is CLI session name, place shows space › tab.
        rule.onNodeWithText("CLI session name").assertExists()
        rule.onNodeWithText("MacBook · persona › Export work", substring = true).assertExists()

        // Enabled: main title is herdr tab name, detail line shows CLI session name.
        rule.runOnIdle { preferTab = true }
        rule.waitForIdle()
        rule.onNodeWithText("Export work").assertExists()
        rule.onNodeWithText("CLI session name · MacBook · persona", substring = true).assertExists()

        // Open More actions -> Rename: single "Name" field, saves both tab_name and title for Claude.
        rule.onNodeWithContentDescription("More actions").performClick()
        rule.onNodeWithText("Rename").performClick()
        rule.onNodeWithText("Name").assertExists()
        rule.onNodeWithText("Tab name (herdr tab)").assertDoesNotExist()
        rule.onNodeWithText("Session name").assertDoesNotExist()
        rule.onNode(hasSetTextAction() and hasText("Export work")).performTextClearance()
        rule.onNode(hasSetTextAction()).performTextInput("Updated export")
        rule.onNodeWithText("Save").assertIsEnabled().performClick()
        rule.waitForIdle()
        assertEquals(listOf(SessionRename(title = "Updated export", tabName = "Updated export")), calls)

        // OpenCode session: unified Rename sends tab_name only.
        calls.clear()
        rule.runOnIdle { current = herdrSession.copy(sessionId = "opencode-1", agent = "opencode") }
        rule.waitForIdle()
        rule.onNodeWithContentDescription("More actions").performClick()
        rule.onNodeWithText("Rename").performClick()
        rule.onNode(hasSetTextAction() and hasText("Export work")).performTextClearance()
        rule.onNode(hasSetTextAction()).performTextInput("Tab only")
        rule.onNodeWithText("Save").assertIsEnabled().performClick()
        rule.waitForIdle()
        assertEquals(listOf(SessionRename(title = null, tabName = "Tab only")), calls)
    }

    @Test fun sessionOverviewHonorsPreferHerdrTabName() {
        var preferTab by mutableStateOf(false)
        val machine = Machine("mac", name = "MacBook", online = true)
        rule.setContent {
            val scope = rememberCoroutineScope()
            val chrome = ChatChromeState(rule.activity.resources, null, scope, {}, {})
            PersonaTheme(reduceMotion = true) {
                SessionOverviewSheet(
                    session = herdrSession,
                    machine = machine,
                    machines = listOf(machine),
                    childRows = emptyList(),
                    state = chrome,
                    preferHerdrTabName = preferTab,
                    onDismiss = {},
                )
            }
        }
        rule.onNodeWithText("CLI session name").assertExists()
        rule.onNodeWithText("Export work").assertDoesNotExist()

        rule.runOnIdle { preferTab = true }
        rule.waitForIdle()
        rule.onNodeWithText("Export work").assertExists()
        rule.onNodeWithText("CLI session name").assertExists()
    }
}
