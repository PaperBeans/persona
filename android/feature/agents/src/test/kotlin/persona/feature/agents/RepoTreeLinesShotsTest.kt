package persona.feature.agents

import androidx.activity.ComponentActivity
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertHasClickAction
import androidx.compose.ui.test.longClick
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performTouchInput
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

/**
 * By Repository on the Agents page (#4338 A2, A7): two computers, a repository with its main checkout and three linked
 * worktrees (one named by its host, one on a detached HEAD), a repository with its main checkout alone, a quiet one, and a
 * worktree on a computer that is offline. Every style, light and dark; PNGs in `build/repo-tree-lines-shots/`.
 */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en-w400dp-h1400dp")
@org.robolectric.annotation.GraphicsMode(org.robolectric.annotation.GraphicsMode.Mode.NATIVE)
class RepoTreeLinesShotsTest {
    @get:Rule val rule = createAndroidComposeRule<ComponentActivity>()

    private val repo = "/home/me/code/persona"
    private val sessions = listOf(
        """"machine_id":"laptop","session_id":"login","title":"Fix the login bug","state":"working","cwd":"$repo","repo_root":"$repo","worktree":"$repo","branch":"main"""",
        """"machine_id":"laptop","session_id":"notes","title":"Review the release notes","state":"unread","cwd":"$repo","repo_root":"$repo","worktree":"$repo","branch":"main"""",
        """"machine_id":"laptop","session_id":"sync","title":"Approve the migration","state":"needs_you","cwd":"$repo/.worktrees/fix-sync","repo_root":"$repo","worktree":"$repo/.worktrees/fix-sync","branch":"fix/sync"""",
        """"machine_id":"laptop","session_id":"csv","title":"Profile the CSV parser","state":"working","cwd":"/home/me/.paseo/worktrees/abc/brave-fox","repo_root":"$repo","worktree":"/home/me/.paseo/worktrees/abc/brave-fox","branch":"perf/importer","slot_name":"Speed up the importer"""",
        """"machine_id":"laptop","session_id":"bisect","title":"Bisect the crash","state":"unread","cwd":"$repo/.worktrees/bisect","repo_root":"$repo","worktree":"$repo/.worktrees/bisect"""",
        """"machine_id":"laptop","session_id":"minutes","title":"Summarize the meeting","state":"unread","cwd":"/home/me/notes"""",
        """"machine_id":"laptop","session_id":"cleanup","title":"Clean up the scripts","state":"idle","cwd":"/home/me/code/scripts"""",
        """"machine_id":"box","session_id":"hotfix","title":"Deploy the hotfix","state":"working","cwd":"/srv/api/.worktrees/hotfix","repo_root":"/srv/api","worktree":"/srv/api/.worktrees/hotfix","branch":"hotfix"""",
    ).map { JsonCodec.decodeFromString(Session.serializer(), """{$it,"updated_at":"2026-01-01T00:00:00Z"}""") }

    /**
     * The lines are buttons TalkBack can press and read as one: a repository's header folds on a tap and opens its actions
     * on a long press (here its agents are in no space: Mark all as read); a worktree's line says what it shows.
     */
    @Test fun theLinesAreButtonsWithTheirWordsAndTheHeaderHoldsTheActions() {
        shoot(AppStyle.Glass, dark = false, save = false)
        rule.onNodeWithContentDescription("persona, Repository", substring = true).assertHasClickAction()
            .assert(SemanticsMatcher.keyIsDefined(SemanticsActions.OnLongClick))
        rule.onNodeWithContentDescription("Worktree, bisect, Detached HEAD, 1 agent").assertHasClickAction()
        rule.onNodeWithContentDescription("Main worktree, main, Primary, 2 agents").assertHasClickAction()
        rule.onNodeWithContentDescription("Worktree, Speed up the importer, perf/importer, 1 agent").assertExists()
        rule.onNodeWithContentDescription("Worktree, hotfix, This computer is offline, 1 agent").assertExists()
        rule.onNodeWithContentDescription("scripts, Repository, 1 agent", substring = true).assertExists()
        rule.onNodeWithContentDescription("persona, Repository", substring = true).performTouchInput { longClick() }
        rule.waitForIdle()
        rule.onNodeWithText("Mark all as read").assertExists()
    }

    @Test fun glassLight() = shoot(AppStyle.Glass, dark = false)
    @Test fun glassDark() = shoot(AppStyle.Glass, dark = true)
    @Test fun briefLight() = shoot(AppStyle.Brief, dark = false)
    @Test fun briefDark() = shoot(AppStyle.Brief, dark = true)
    @Test fun boardLight() = shoot(AppStyle.Board, dark = false)
    @Test fun boardDark() = shoot(AppStyle.Board, dark = true)

    private fun shoot(style: AppStyle, dark: Boolean, save: Boolean = true) {
        val model = AgentsViewModel(object : SessionsRepository {
            override val sessions = MutableStateFlow(this@RepoTreeLinesShotsTest.sessions)
            override val machines = MutableStateFlow(listOf(Machine("laptop", "Laptop", online = true),
                Machine("box", "Build server", online = false, icon = "server")))
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
        if (!save) return
        val out = File(System.getProperty("persona.shots") ?: "build/repo-tree-lines-shots").also { it.mkdirs() }
        rule.onRoot().captureToImage().asAndroidBitmap().let { bitmap ->
            out.resolve("repo-tree-${style.name.lowercase()}-${if (dark) "dark" else "light"}.png").outputStream()
                .use { bitmap.compress(android.graphics.Bitmap.CompressFormat.PNG, 100, it) }
        }
    }

    private object Nowhere : Navigator {
        override fun navigate(key: NavKey) = Unit
        override fun back() = Unit
        override fun selectTab(tab: AppTab) = Unit
    }
}
