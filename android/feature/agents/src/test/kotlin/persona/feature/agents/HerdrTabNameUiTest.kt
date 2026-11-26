package persona.feature.agents

import androidx.activity.ComponentActivity
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.test.assertIsEnabled
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextClearance
import androidx.compose.ui.test.performTextInput
import androidx.compose.ui.test.performTextReplacement
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.emptyPreferences
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.serialization.json.JsonObject
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.Interaction
import persona.core.contracts.Machine
import persona.core.contracts.Resolution
import persona.core.contracts.ServerEvent
import persona.core.contracts.Session
import persona.core.contracts.SessionKey
import persona.core.contracts.api.AgentOperationPreferences
import persona.core.contracts.api.AgentOperations
import persona.core.contracts.api.AssistantConversations
import persona.core.contracts.api.AssistantSnapshot
import persona.core.contracts.api.ConnectionState
import persona.core.contracts.api.HubConnection
import persona.core.contracts.api.HubPreferences
import persona.core.contracts.api.HubPreferencesSnapshot
import persona.core.contracts.api.InteractionCenter
import persona.core.contracts.api.MessageSender
import persona.core.contracts.api.OutboxMessage
import persona.core.contracts.api.SessionsRepository
import persona.core.designsystem.AppStyle
import persona.core.designsystem.GroupPosition
import persona.core.designsystem.PersonaTheme
import persona.core.domain.sort.AgentSession
import persona.core.domain.sort.SessionRename
import java.time.Instant

@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en")
class HerdrTabNameUiTest {
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

    @Test fun slotRowShowsTabNameAsMainTitleAndCliTitleAsDetailWhenEnabled() {
        var preferTab by mutableStateOf(false)
        var style by mutableStateOf(AppStyle.Glass)
        rule.setContent {
            PersonaTheme(style = style, reduceMotion = true) {
                SlotRow(
                    row = AgentSession(herdrSession),
                    machine = "MacBook",
                    metadata = AgentRowMetadata(),
                    now = Instant.parse("2026-11-23T12:00:00Z"),
                    compact = false,
                    pinned = false,
                    muted = false,
                    depth = 0,
                    position = GroupPosition.Single,
                    located = true,
                    onOpen = {},
                    menuItems = null,
                    mixesComputers = true,
                    preferHerdrTabName = preferTab,
                )
            }
        }
        rule.onNodeWithText("CLI session name", substring = false).assertExists()
        rule.onNodeWithText("persona › Export work", substring = true).assertExists()

        rule.runOnIdle { preferTab = true }
        rule.waitForIdle()
        rule.onNodeWithText("Export work", substring = false).assertExists()
        rule.onNodeWithText("CLI session name · persona · MacBook", substring = true).assertExists()

        // Brief and Board also promote the herdr tab name and surface the CLI title in facts.
        for (otherStyle in listOf(AppStyle.Brief, AppStyle.Board)) {
            rule.runOnIdle { style = otherStyle }
            rule.waitForIdle()
            rule.onNodeWithText("Export work", substring = false).assertExists()
            rule.onNodeWithText("CLI session name", substring = true).assertExists()
        }
    }

    @Test fun renameDialogMergesIntoOneNameFieldWhenEnabledAndKeepsTwoFieldsWhenOff() {
        val renames = mutableListOf<SessionRename>()
        val model = viewModelRecording(renames)
        var preferTab by mutableStateOf(true)
        var target by mutableStateOf(herdrSession)
        rule.setContent {
            PersonaTheme(reduceMotion = true) {
                AgentOperationDialog(
                    dialog = AgentOperationDialog.Rename(target),
                    model = model,
                    machineName = "MacBook",
                    preferHerdrTabName = preferTab,
                    dismiss = {},
                )
            }
        }

        // Unified mode for Claude: one "Name" field, sends both tab_name and title.
        rule.onNodeWithText("Name").assertExists()
        rule.onNodeWithText("Tab name (herdr tab)").assertDoesNotExist()
        rule.onNodeWithText("Session name").assertDoesNotExist()
        rule.onNode(hasSetTextAction() and hasText("Export work")).performTextClearance()
        rule.onNode(hasSetTextAction()).performTextInput("Updated export")
        rule.onNodeWithText("Save").assertIsEnabled().performClick()
        rule.waitForIdle()
        assertEquals(listOf(SessionRename(title = "Updated export", tabName = "Updated export")), renames)

        // Unified mode for opencode (no native CLI rename): sends tab_name only.
        renames.clear()
        rule.runOnIdle { target = herdrSession.copy(sessionId = "opencode-1", agent = "opencode") }
        rule.waitForIdle()
        rule.onNodeWithText("Name").assertExists()
        rule.onNode(hasSetTextAction() and hasText("Export work")).performTextClearance()
        rule.onNode(hasSetTextAction()).performTextInput("Tab only")
        rule.onNodeWithText("Save").assertIsEnabled().performClick()
        rule.waitForIdle()
        assertEquals(listOf(SessionRename(title = null, tabName = "Tab only")), renames)

        // Setting off: keeps separate Tab name and Session name fields and sends only the edited field.
        renames.clear()
        rule.runOnIdle {
            preferTab = false
            target = herdrSession.copy(sessionId = "off-1")
        }
        rule.waitForIdle()
        rule.onNodeWithText("Tab name (herdr tab)").assertExists()
        rule.onNodeWithText("Session name").assertExists()
        rule.onNode(hasSetTextAction() and hasText("CLI session name")).performTextReplacement("CLI only")
        rule.onNodeWithText("Save").assertIsEnabled().performClick()
        rule.waitForIdle()
        assertEquals(listOf(SessionRename(title = "CLI only", tabName = null)), renames)
    }

    @Test fun glassNeedsYouWaitingRowShowsHerdrTabNameWhenEnabled() {
        var preferTab by mutableStateOf(false)
        rule.setContent {
            PersonaTheme(style = AppStyle.Glass, reduceMotion = true) {
                GlassNeedsYouWaitingRow(
                    owner = herdrSession,
                    machine = "MacBook",
                    sidebar = false,
                    preferHerdrTabName = preferTab,
                    onClick = {},
                )
            }
        }
        rule.onNodeWithText("CLI session name · MacBook").assertExists()

        rule.runOnIdle { preferTab = true }
        rule.waitForIdle()
        rule.onNodeWithText("Export work · MacBook").assertExists()
    }

    private fun viewModelRecording(renames: MutableList<SessionRename>): AgentsViewModel {
        val repo = object : SessionsRepository {
            override val sessions = MutableStateFlow(listOf(herdrSession))
            override val machines = MutableStateFlow(emptyList<Machine>())
            override suspend fun refresh() = Unit
        }
        val interactions = object : InteractionCenter {
            override val pending = MutableStateFlow(emptyList<Interaction>())
            override suspend fun refresh() = Unit
            override suspend fun resolve(id: String, resolution: Resolution) = Unit
        }
        val connection = object : HubConnection {
            override val state = MutableStateFlow(ConnectionState.Online)
            override val events = MutableSharedFlow<ServerEvent>()
            override fun reconnect() = Unit
            override fun send(message: JsonObject) = true
        }
        val sender = object : MessageSender {
            override val outbox = MutableStateFlow(emptyList<OutboxMessage>())
            override suspend fun send(session: SessionKey, text: String, clientMessageId: String, clearDraft: String?) = Unit
        }
        val store = object : DataStore<Preferences> {
            override val data = MutableStateFlow<Preferences>(emptyPreferences())
            override suspend fun updateData(transform: suspend (Preferences) -> Preferences) =
                transform(data.value).also { data.value = it }
        }
        val hubPrefs = object : HubPreferences {
            override val preferences = MutableStateFlow(HubPreferencesSnapshot())
            override suspend fun refresh() = Unit
            override suspend fun togglePin(path: String, space: Boolean) = Unit
            override suspend fun toggleMute(path: String, space: Boolean) = Unit
            override suspend fun markSeen(session: SessionKey, version: String) = Unit
        }
        val ops = object : AgentOperations {
            override val scope = "fixture"
            override suspend fun pins() = AgentOperationPreferences()
            override suspend fun mutes() = AgentOperationPreferences()
            override suspend fun savePins(value: AgentOperationPreferences) = Unit
            override suspend fun saveMutes(value: AgentOperationPreferences) = Unit
            override suspend fun renameSession(key: SessionKey, title: String?, tabName: String?) {
                renames += SessionRename(title = title, tabName = tabName)
            }
            override suspend fun renameSpace(machineId: String, workspaceId: String, name: String) = Unit
            override suspend fun closeSession(key: SessionKey) = Unit
            override suspend fun attachSession(key: SessionKey) = Unit
            override suspend fun interruptSession(key: SessionKey) = Unit
            override suspend fun deleteSession(key: SessionKey) = Unit
            override suspend fun forkSession(key: SessionKey) = "forked"
        }
        val assistant = object : AssistantConversations {
            override val assistant = MutableStateFlow(AssistantSnapshot())
            override val pinned = MutableStateFlow(emptySet<SessionKey>())
            override suspend fun startConversation() = SessionKey("mac", "assistant")
            override suspend fun refresh() = Unit
        }
        return AgentsViewModel(
            repository = repo,
            interactions = interactions,
            connection = connection,
            sender = sender,
            preferences = AgentsPreferences(store, hubPrefs),
            bindings = AgentsOperationsBindings(ops, assistant),
        )
    }
}
