package persona.feature.agents

import android.graphics.Bitmap
import androidx.activity.ComponentActivity
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onRoot
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.emptyPreferences
import androidx.navigation3.runtime.NavKey
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.serialization.json.JsonObject
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.*
import persona.core.contracts.api.*
import persona.core.designsystem.AppStyle
import persona.core.designsystem.HomeChrome
import persona.core.designsystem.LocalHomeChrome
import persona.core.designsystem.PersonaTheme
import persona.core.domain.sort.AgentGrouping
import persona.core.domain.sort.AgentListDisplay
import persona.core.domain.sort.AgentQuickFilter
import persona.core.navigation.AppTab
import persona.core.navigation.Navigator
import java.io.File

/**
 * Waiting in the background's lighter tone (#4338 A5, A11) on two computers' agents, two of them waiting in the background:
 * By Status (the section's clock and count), then By project under the chosen "Waiting in the background" filter (the chip,
 * the computers' clocks, the rows' clocks and state word), and on Glass that list as Two lines and State cards. Every style,
 * light and dark: Brief and Board must draw as before. PNGs in `build/background-tone-shots/` (or `-Dpersona.shots=<dir>`).
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h1000dp-xhdpi")
class BackgroundToneShotsTest {
    @get:Rule val rule = createAndroidComposeRule<ComponentActivity>()

    private fun session(machine: String, id: String, title: String, state: String, words: String, agent: String = "claude",
                        waiting: Boolean = false) =
        Session(machine, id, title = title, cwd = "/code/app", state = state, agent = agent, spaceId = "w1", spaceName = "app",
            slotName = id, paneId = "w1:$id", lastText = words, backgroundWaiting = waiting, backgroundWorking = waiting,
            updatedAt = "2026-01-01T00:00:00Z")

    private val sessions = listOf(
        session("mac", "needs", "Approve the migration", "needs_you", "Run the migration on the staging database?"),
        session("mac", "working", "Fix the login bug", "working", "Updating the retry logic."),
        session("mac", "watch", "Watch the CI run", "working", "Waiting for the CI run to finish.", waiting = true),
        session("server", "nightly", "Nightly build", "working", "Waiting for the build to finish.", agent = "codex", waiting = true),
        session("server", "docs", "Update the docs", "idle", "The guide is updated."),
    )
    private val machines = listOf(Machine("mac", "MacBook Pro", online = true, icon = "laptop"),
        Machine("server", "Workstation", online = true, icon = "server"))

    @Test fun everyStyle() {
        val model = AgentsViewModel(object : SessionsRepository {
            override val sessions = MutableStateFlow(this@BackgroundToneShotsTest.sessions)
            override val machines = MutableStateFlow(this@BackgroundToneShotsTest.machines)
            override suspend fun refresh() = Unit
        }, object : InteractionCenter {
            override val pending = MutableStateFlow<List<Interaction>>(emptyList())
            override suspend fun refresh() = Unit
            override suspend fun resolve(id: String, resolution: Resolution) = Unit
        }, object : HubConnection {
            override val state = MutableStateFlow(ConnectionState.Online)
            override val events = MutableSharedFlow<ServerEvent>()
            override fun reconnect() = Unit
            override fun send(message: JsonObject) = true
        }, object : MessageSender {
            override val outbox = MutableStateFlow<List<OutboxMessage>>(emptyList())
            override suspend fun send(session: SessionKey, text: String, clientMessageId: String, clearDraft: String?) = Unit
        }, AgentsPreferences(object : DataStore<Preferences> {
            override val data = MutableStateFlow<Preferences>(emptyPreferences())
            override suspend fun updateData(transform: suspend (Preferences) -> Preferences) =
                transform(data.value).also { data.value = it }
        }, object : HubPreferences {
            override val preferences = MutableStateFlow(HubPreferencesSnapshot())
            override suspend fun refresh() = Unit
            override suspend fun togglePin(path: String, space: Boolean) = Unit
            override suspend fun toggleMute(path: String, space: Boolean) = Unit
            override suspend fun markSeen(session: SessionKey, version: String) = Unit
        }))
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                CompositionLocalProvider(LocalHomeChrome provides HomeChrome(emptyList(), "Agents", onSelect = {}, leading = {})) {
                    AgentsScreen(model, Nowhere, AgentsMetadata())
                }
            }
        }
        rule.waitUntil(5_000) { model.page.value.loaded }
        val out = File(System.getProperty("persona.shots") ?: "build/background-tone-shots").also { it.mkdirs() }
        fun save(name: String) {
            rule.waitForIdle()
            val bitmap = rule.onRoot().captureToImage().asAndroidBitmap()
            File(out, "$name-${style.name.lowercase()}-${if (dark) "dark" else "light"}.png").outputStream()
                .use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
        }
        for (s in AppStyle.entries) for (d in listOf(false, true)) {
            rule.runOnIdle { style = s; dark = d; model.filter(AgentQuickFilter.All); model.grouping(AgentGrouping.State) }
            save("by-status")
            rule.runOnIdle { model.grouping(AgentGrouping.MachineSpace); model.filter(AgentQuickFilter.BackgroundWaiting) }
            save("waiting-filter")
            if (s == AppStyle.Glass) {
                rule.runOnIdle { model.display(AgentListDisplay.TwoLines) }
                save("waiting-two-lines")
                rule.runOnIdle { model.display(AgentListDisplay.Cards) }
                save("waiting-cards")
                rule.runOnIdle { model.display(AgentListDisplay.Avatar) }
            }
        }
    }

    private object Nowhere : Navigator {
        override fun navigate(key: NavKey) = Unit
        override fun back() = Unit
        override fun selectTab(tab: AppTab) = Unit
    }
}
