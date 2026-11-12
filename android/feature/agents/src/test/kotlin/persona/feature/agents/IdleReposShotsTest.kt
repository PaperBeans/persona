package persona.feature.agents

import androidx.activity.ComponentActivity
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.hasContentDescription
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
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
import persona.core.contracts.*
import persona.core.contracts.api.*
import persona.core.designsystem.AppStyle
import persona.core.designsystem.HomeChrome
import persona.core.designsystem.LocalHomeChrome
import persona.core.designsystem.PersonaTheme
import persona.core.navigation.AppTab
import persona.core.navigation.Navigator
import java.io.File
import java.time.Duration
import java.time.Instant
import java.time.temporal.ChronoUnit

/**
 * By Repository's idle repositories and idle line on the Agents page (#4338 A8): two computers, each with a busy
 * repository and idle ones; the busy repository's main checkout holds a working agent and two idle ones in their tabs.
 * Every style, light and dark, as it opens and with an idle repository and the idle agents opened; PNGs in
 * `build/idle-repos-shots/`.
 */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en-w400dp-h1700dp")
@org.robolectric.annotation.GraphicsMode(org.robolectric.annotation.GraphicsMode.Mode.NATIVE)
class IdleReposShotsTest {
    @get:Rule val rule = createAndroidComposeRule<ComponentActivity>()

    // Whole hours ago, at least one, so the words stay the same through the hour.
    private val now = Instant.now().truncatedTo(ChronoUnit.HOURS)
    private fun ago(hours: Long) = now.minus(Duration.ofHours(hours)).toString()
    private fun at(machine: String, id: String, title: String, state: String, cwd: String, hoursAgo: Long, tab: String? = null) =
        """"machine_id":"$machine","session_id":"$id","title":"$title","state":"$state","cwd":"$cwd","updated_at":"${ago(hoursAgo)}"""" +
            (tab?.let { ""","slot_id":"$it","slot_name":"$it"""" } ?: "")
    private val sessions = listOf(
        at("laptop", "build", "Fix the login bug", "working", "/home/me/code/persona", 1, tab = "build"),
        at("laptop", "review", "Review the release notes", "idle", "/home/me/code/persona", 3, tab = "review"),
        at("laptop", "research", "Compare the cache libraries", "idle", "/home/me/code/persona", 26, tab = "research"),
        at("laptop", "fix", "Bisect the crash", "unread", "/home/me/code/persona/.worktrees/bisect", 1, tab = "bisect"),
        at("laptop", "minutes", "Summarize the meeting", "idle", "/home/me/notes", 2, tab = "minutes"),
        at("laptop", "plan", "Plan the week", "idle", "/home/me/notes", 30, tab = "planning"),
        at("laptop", "cleanup", "Clean up the scripts", "idle", "/home/me/code/scripts", 75),
        at("box", "deploy", "Deploy the hotfix", "working", "/srv/api", 1, tab = "deploy"),
        at("box", "certs", "Rotate the certificates", "idle", "/srv/infra", 120, tab = "certs"),
    ).map { JsonCodec.decodeFromString(Session.serializer(), "{$it}") }

    @Test fun glassLight() = shoot(AppStyle.Glass, dark = false)
    @Test fun glassDark() = shoot(AppStyle.Glass, dark = true)
    @Test fun briefLight() = shoot(AppStyle.Brief, dark = false)
    @Test fun briefDark() = shoot(AppStyle.Brief, dark = true)
    @Test fun boardLight() = shoot(AppStyle.Board, dark = false)
    @Test fun boardDark() = shoot(AppStyle.Board, dark = true)
    @Test fun glassOpenLight() = shoot(AppStyle.Glass, dark = false, open = true)
    @Test fun glassOpenDark() = shoot(AppStyle.Glass, dark = true, open = true)

    /**
     * On Glass the idle line shows the idle agents and then folds them again, and an idle repository's row opens its
     * agents under it and folds them again; each is one button TalkBack reads with its words.
     */
    @Test fun theIdleLineAndAnIdleRepositoryOpenAndFoldAgain() {
        shoot(AppStyle.Glass, dark = false, save = false)
        rule.onNodeWithContentDescription("Show 2 idle agents").performClick()
        rule.waitForIdle()
        rule.onNodeWithContentDescription("Hide 2 Idle").performClick()
        rule.waitForIdle()
        rule.onNodeWithContentDescription("Show 2 idle agents").assertExists()
        rule.onNodeWithContentDescription("notes, Repository, 2 agents · minutes · planning", substring = true).performClick()
        rule.waitForIdle()
        rule.onNode(hasText("Plan the week")).assertExists()
        rule.onNodeWithContentDescription("notes, Repository", substring = true).performClick()
        rule.waitForIdle()
        rule.onNode(hasText("Plan the week")).assertDoesNotExist()
    }

    private fun shoot(style: AppStyle, dark: Boolean, open: Boolean = false, save: Boolean = true) {
        val model = AgentsViewModel(object : SessionsRepository {
            override val sessions = MutableStateFlow(this@IdleReposShotsTest.sessions)
            override val machines = MutableStateFlow(listOf(Machine("laptop", "Laptop", online = true),
                Machine("box", "Build server", online = true, icon = "server")))
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
        rule.setContent {
            PersonaTheme(style = style, darkTheme = dark, reduceMotion = true) {
                CompositionLocalProvider(LocalHomeChrome provides HomeChrome(emptyList(), "Agents", onSelect = {}, leading = {})) {
                    AgentsScreen(model, Nowhere, AgentsMetadata())
                }
            }
        }
        rule.waitUntil(5_000) { model.page.value.loaded }
        rule.waitForIdle()
        if (open) {
            // The idle repository "notes" and the idle agents of persona's main checkout, opened (on main before #4338 A8:
            // the folded repository's header and its "2 more idle" row).
            rule.onNodeWithContentDescription("notes, Repository", substring = true).performClick()
            rule.onNode(hasContentDescription("Show 2 idle agents") or hasText("2 more idle")).performClick()
            rule.waitForIdle()
        }
        if (!save) return
        val out = File(System.getProperty("persona.shots") ?: "build/idle-repos-shots").also { it.mkdirs() }
        rule.onRoot().captureToImage().asAndroidBitmap().let { bitmap ->
            out.resolve("idle-repos-${style.name.lowercase()}${if (open) "-open" else ""}-${if (dark) "dark" else "light"}.png").outputStream()
                .use { bitmap.compress(android.graphics.Bitmap.CompressFormat.PNG, 100, it) }
        }
    }

    private object Nowhere : Navigator {
        override fun navigate(key: NavKey) = Unit
        override fun back() = Unit
        override fun selectTab(tab: AppTab) = Unit
    }
}
