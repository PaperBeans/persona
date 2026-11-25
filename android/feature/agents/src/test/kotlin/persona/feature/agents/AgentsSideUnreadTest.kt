package persona.feature.agents

import android.graphics.Bitmap
import androidx.activity.ComponentActivity
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.*
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.unit.dp
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.emptyPreferences
import androidx.lifecycle.viewModelScope
import androidx.navigation3.runtime.NavKey
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.JsonObject
import org.junit.After
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.*
import persona.core.contracts.api.*
import persona.core.designsystem.*
import persona.core.domain.sort.*
import persona.core.navigation.ChatRoute
import persona.core.navigation.Navigator
import java.io.File
import java.time.Instant

/**
 * End-to-end Agents list tests for parent rows kept Unread by folded side sessions (#13524):
 * naming the most recent unread side (+N for others), one-tap navigation to that side, keeping
 * the parent Unread after its own seen mark while a side is unread, dropping it from Unread
 * counts once held sides are seen, and leaving rows without unread sides unchanged.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h900dp-xhdpi")
class AgentsSideUnreadTest {
    @get:Rule val rule = createAndroidComposeRule<ComponentActivity>()
    private val models = mutableListOf<AgentsViewModel>()
    @After fun disposeModels() { models.forEach { it.viewModelScope.cancel() } }

    private val hub = MutableStateFlow(HubPreferencesSnapshot())
    private val sessions = MutableStateFlow<List<Session>>(emptyList())
    private val listStatus = MutableStateFlow(SessionListStatus(loaded = true))
    private val navigated = mutableListOf<NavKey>()

    private val navigator = object : Navigator {
        override fun navigate(key: NavKey) { navigated += key }
        override fun back() = Unit
        override fun selectTab(tab: persona.core.navigation.AppTab) = Unit
    }

    private fun preferences(): AgentsPreferences = AgentsPreferences(object : DataStore<Preferences> {
        override val data = MutableStateFlow(emptyPreferences())
        override suspend fun updateData(transform: suspend (Preferences) -> Preferences) =
            transform(data.value).also { data.value = it }
    }, object : HubPreferences {
        override val preferences = hub
        override suspend fun refresh() = Unit
        override suspend fun togglePin(path: String, space: Boolean) = Unit
        override suspend fun toggleMute(path: String, space: Boolean) = Unit
        override suspend fun markSeen(session: SessionKey, version: String) {
            hub.value = hub.value.copy(seen = hub.value.seen + (session.path to version))
        }
    }).also { runBlocking { it.grouping(AgentGrouping.State) } }

    private fun model(prefs: AgentsPreferences): AgentsViewModel = AgentsViewModel(
        repository = object : SessionsRepository {
            override val sessions = this@AgentsSideUnreadTest.sessions
            override val machines = MutableStateFlow(listOf(Machine("mac", "Studio", online = true)))
            override val listStatus = this@AgentsSideUnreadTest.listStatus
            override fun supportsFeature(machineId: String, feature: String) = feature == "sessions_hierarchy"
            override suspend fun refresh() = Unit
        },
        interactions = object : InteractionCenter {
            override val pending = MutableStateFlow<List<Interaction>>(emptyList())
            override suspend fun refresh() = Unit
            override suspend fun resolve(id: String, resolution: Resolution) = Unit
        },
        connection = object : HubConnection {
            override val state = MutableStateFlow(ConnectionState.Online)
            override val events = MutableSharedFlow<ServerEvent>()
            override fun reconnect() = Unit
            override fun send(message: JsonObject) = true
        },
        sender = object : MessageSender {
            override val outbox = MutableStateFlow<List<OutboxMessage>>(emptyList())
            override suspend fun send(session: SessionKey, text: String, clientMessageId: String, clearDraft: String?) = Unit
        },
        preferences = prefs,
    ).also { models += it }

    private fun parent(state: String = "idle", sidesUnread: Int = 2, sides: Int = 3) = Session(
        machineId = "mac",
        sessionId = "review",
        title = "Review PR",
        state = state,
        status = if (state == "unread") "done" else "idle",
        lastText = "Parent summary",
        cwd = "/repo/app",
        updatedAt = "2026-11-15T10:02:00Z",
        children = SessionChildren(total = sides, sides = sides, sidesUnread = sidesUnread),
    )

    private fun side(id: String, title: String, state: String, updatedAt: String) = Session(
        machineId = "mac",
        sessionId = id,
        title = title,
        state = state,
        status = if (state == "unread") "done" else "idle",
        lastText = "Reply from $id",
        cwd = "/repo/app",
        updatedAt = updatedAt,
        foldedUnder = "review",
        foldedAs = FoldedAs.SIDE,
        parentAgentId = "review",
    )

    @Test fun namesNewestUnreadSideWithPlusNReplacesSeenPreviewAndOpensSideInOneTap() {
        val prefs = preferences()
        val older = side("btw", "btw question", "unread", "2026-11-15T10:03:00Z")
        val newer = side("chat", "Side chat", "unread", "2026-11-15T10:05:00Z")
        val seen = side("old", "Seen side", "idle", "2026-11-15T10:01:00Z")
        sessions.value = listOf(parent(), older, newer, seen)

        val vm = model(prefs)
        rule.setContent { PersonaTheme(reduceMotion = true) { AgentsScreen(vm, navigator, AgentsMetadata()) } }
        rule.waitUntil(5_000) { vm.page.value.loaded && vm.page.value.list.counts[AgentQuickFilter.Unread] == 1 }
        rule.waitForIdle()

        // Parent's own already-seen words do not stand under the side's green dot.
        rule.onNodeWithText("Review PR", useUnmergedTree = true).assertIsDisplayed()
        rule.onNodeWithText("Parent summary", substring = true, useUnmergedTree = true).assertDoesNotExist()
        rule.onNodeWithText("Side chat", substring = true, useUnmergedTree = true).assertIsDisplayed()
        rule.onNodeWithText("+1", useUnmergedTree = true).assertIsDisplayed()

        // Tapping the unread side line opens that side session directly in one tap, not the parent.
        rule.onNodeWithText("Side chat", substring = true, useUnmergedTree = true).performClick()
        rule.runOnIdle {
            assertEquals(listOf(ChatRoute("mac", "chat")), navigated)
        }

        // Tapping the parent title opens the parent row as before.
        rule.onNodeWithText("Review PR", useUnmergedTree = true).performClick()
        rule.runOnIdle {
            assertEquals(listOf(ChatRoute("mac", "chat"), ChatRoute("mac", "review")), navigated)
        }

        // Seeing the newest side moves the line to the remaining unread side (no +1), keeping Unread count = 1.
        runBlocking { prefs.markSeen(listOf(newer)) }
        rule.waitUntil(5_000) {
            rule.onAllNodesWithText("btw question", substring = true, useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()
        }
        assertEquals(1, vm.page.value.list.counts[AgentQuickFilter.Unread])
        rule.onNodeWithText("+1", useUnmergedTree = true).assertDoesNotExist()

        // Seeing the last unread side takes the parent out of Unread counts and removes the side line.
        runBlocking { prefs.markSeen(listOf(older)) }
        rule.waitUntil(5_000) { vm.page.value.list.counts[AgentQuickFilter.Unread] == 0 }
        rule.onNodeWithText("btw question", substring = true, useUnmergedTree = true).assertDoesNotExist()
    }

    @Test fun keepsParentUnreadAfterParentsOwnSeenMarkWhileASideIsUnread() {
        val prefs = preferences()
        val p = parent(state = "unread", sidesUnread = 1, sides = 1)
        val s = side("chat", "Side chat", "unread", "2026-11-15T10:05:00Z")
        sessions.value = listOf(p, s)

        val vm = model(prefs)
        rule.setContent { PersonaTheme(reduceMotion = true) { AgentsScreen(vm, navigator, AgentsMetadata()) } }
        rule.waitUntil(5_000) { vm.page.value.loaded && vm.page.value.list.counts[AgentQuickFilter.Unread] == 1 }
        rule.waitForIdle()

        // Both parent and side are unread: both lines show.
        rule.onNodeWithText("Parent summary", substring = true, useUnmergedTree = true).assertIsDisplayed()
        rule.onNodeWithText("Side chat", substring = true, useUnmergedTree = true).assertIsDisplayed()

        // Marking only the parent seen keeps the parent Unread (count stays 1), hides parent's now-seen preview,
        // and continues naming the unread side.
        runBlocking { prefs.markSeen(listOf(p)) }
        rule.waitUntil(5_000) {
            rule.onAllNodesWithText("Parent summary", substring = true, useUnmergedTree = true).fetchSemanticsNodes().isEmpty()
        }
        assertEquals(1, vm.page.value.list.counts[AgentQuickFilter.Unread])
        rule.onNodeWithText("Side chat", substring = true, useUnmergedTree = true).assertIsDisplayed()
    }

    @Test fun rowWithNoUnreadSideStaysUnchanged() {
        val prefs = preferences()
        val p = parent(state = "unread", sidesUnread = 0, sides = 1)
        val seen = side("old", "Seen side", "idle", "2026-11-15T10:01:00Z")
        sessions.value = listOf(p, seen)

        val vm = model(prefs)
        rule.setContent { PersonaTheme(reduceMotion = true) { AgentsScreen(vm, navigator, AgentsMetadata()) } }
        rule.waitUntil(5_000) { vm.page.value.loaded && vm.page.value.list.counts[AgentQuickFilter.Unread] == 1 }
        rule.waitForIdle()

        rule.onNodeWithText("Review PR", useUnmergedTree = true).assertIsDisplayed()
        rule.onNodeWithText("Parent summary", substring = true, useUnmergedTree = true).assertIsDisplayed()
        rule.onNodeWithText("Seen side", substring = true, useUnmergedTree = true).assertDoesNotExist()
        rule.onNodeWithText("New side reply", substring = true, useUnmergedTree = true).assertDoesNotExist()
    }

    @Test fun pagedParentShowsCountOnlyBeforeSideRowsLoadAndKeepsUnreadCountAfterParentSeen() {
        val prefs = preferences()
        val p = parent(state = "unread", sidesUnread = 2, sides = 2)
        sessions.value = listOf(p)
        val hubCounts = SessionListCounts(
            total = 1,
            machine = mapOf("mac" to 1),
            state = mapOf("unread" to 1),
            revision = "r1",
        )
        listStatus.value = SessionListStatus(
            paged = true,
            loaded = true,
            total = 1,
            counts = hubCounts,
            machineCounts = hubCounts,
        )

        val vm = model(prefs)
        rule.setContent { PersonaTheme(reduceMotion = true) { AgentsScreen(vm, navigator, AgentsMetadata()) } }
        rule.waitUntil(5_000) { vm.page.value.loaded && vm.page.value.list.counts[AgentQuickFilter.Unread] == 1 }
        rule.waitForIdle()

        rule.onNodeWithText("2 side sessions have new replies", useUnmergedTree = true).assertIsDisplayed()

        // Tapping the count-only line falls through to the parent row (not clickable on its own).
        rule.onNodeWithText("2 side sessions have new replies", useUnmergedTree = true).performClick()
        rule.runOnIdle {
            assertEquals(listOf(ChatRoute("mac", "review")), navigated)
        }

        // Marking only the parent seen keeps the parent Unread in paged counts too.
        runBlocking { prefs.markSeen(listOf(p)) }
        rule.waitUntil(5_000) {
            rule.onAllNodesWithText("Parent summary", substring = true, useUnmergedTree = true).fetchSemanticsNodes().isEmpty()
        }
        assertEquals(1, vm.page.value.list.counts[AgentQuickFilter.Unread])
        rule.onNodeWithText("2 side sessions have new replies", useUnmergedTree = true).assertIsDisplayed()
    }

    @Test fun rendersUnreadSideLineCleanlyAcrossAllStylesAndThemes() {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        val rawSeen = parent(state = "idle", sidesUnread = 2, sides = 3)
        val rawUnread = parent(state = "unread", sidesUnread = 1, sides = 1)
        val older = side("btw", "btw question", "unread", "2026-11-15T10:03:00Z")
        val newer = side("chat", "Side chat", "unread", "2026-11-15T10:05:00Z")
        val plain = Session("mac", "plain", title = "Plain unread", state = "unread", status = "done",
            lastText = "Finished the cleanup", cwd = "/repo/app", updatedAt = "2026-11-15T10:04:00Z")
        val foldedSeen = foldRows(listOf(rawSeen, older, newer).map(::AgentSession)).single()
        val foldedBoth = foldRows(listOf(rawUnread, newer).map(::AgentSession)).single()
        val plainRow = AgentSession(plain)
        val summarySeen = unreadSideSummary(foldedSeen.session, rawSeen, listOf(rawSeen, older, newer))
        val summaryBoth = unreadSideSummary(foldedBoth.session, rawUnread, listOf(rawUnread, newer))
        val now = Instant.parse("2026-11-15T10:06:00Z")

        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                Column(Modifier.testTag("shot").width(400.dp).background(PersonaTheme.palette.background).padding(Tokens.Space.l)) {
                    SlotRow(foldedSeen, "Studio", AgentRowMetadata(), now, false, false, false, 0,
                        GroupPosition.of(0, 3), false, {}, menuItems = null, sideUnread = summarySeen, onOpenSide = {})
                    SlotRow(foldedBoth, "Studio", AgentRowMetadata(), now, false, false, false, 0,
                        GroupPosition.of(1, 3), false, {}, menuItems = null, sideUnread = summaryBoth, onOpenSide = {})
                    SlotRow(plainRow, "Studio", AgentRowMetadata(), now, false, false, false, 0,
                        GroupPosition.of(2, 3), false, {}, menuItems = null, sideUnread = null, onOpenSide = {})
                }
            }
        }
        val out = File(System.getProperty("persona.shots") ?: "build/side-unread-shots").also { it.mkdirs() }
        for (s in AppStyle.entries) {
            val colors = mutableListOf<Int>()
            for (d in listOf(false, true)) {
                style = s; dark = d
                Snapshot.sendApplyNotifications()
                rule.waitForIdle()
                rule.onNodeWithText("Plain unread", useUnmergedTree = true).assertIsDisplayed()
                rule.onNodeWithText("Finished the cleanup", substring = true, useUnmergedTree = true).assertIsDisplayed()
                rule.onNodeWithText("+1", useUnmergedTree = true).assertIsDisplayed()
                val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
                colors += bitmap.getPixel(2, 2)
                out.resolve("side-unread-${s.name.lowercase()}-${if (d) "dark" else "light"}.png").outputStream().use {
                    bitmap.compress(Bitmap.CompressFormat.PNG, 100, it)
                }
            }
            assertNotEquals(colors[0], colors[1])
        }
    }
}
