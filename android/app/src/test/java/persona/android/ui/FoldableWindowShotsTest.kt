@file:OptIn(androidx.compose.material3.adaptive.ExperimentalMaterial3AdaptiveApi::class)

package persona.android.ui

import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.material3.adaptive.currentWindowAdaptiveInfo
import androidx.compose.material3.adaptive.layout.calculatePaneScaffoldDirectiveWithTwoPanesOnMediumWidth
import androidx.compose.material3.adaptive.navigation3.ListDetailSceneStrategy
import androidx.compose.ui.platform.LocalWindowInfo
import androidx.compose.ui.unit.dp
import persona.android.navigation.*
import persona.feature.assistant.AssistantSidebarContent

import android.app.Application
import android.content.ComponentName
import android.graphics.Bitmap
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.animation.ContentTransform
import androidx.compose.animation.EnterTransition
import androidx.compose.animation.ExitTransition
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.runtime.*
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createEmptyComposeRule
import androidx.compose.ui.unit.Density
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.emptyPreferences
import androidx.lifecycle.viewmodel.navigation3.rememberViewModelStoreNavEntryDecorator
import androidx.navigation3.runtime.*
import androidx.navigation3.ui.NavDisplay
import androidx.test.core.app.ActivityScenario
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.serialization.json.JsonObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.After
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Shadows
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.android.navigation.AppNavigator
import persona.core.contracts.*
import persona.core.contracts.api.*
import persona.core.designsystem.*
import persona.core.navigation.*
import persona.feature.agents.*
import persona.feature.chat.ChatScreen
import java.io.File

/** Regular-window columns with the real Assistant sidebar, Agents list, chat and Navigation 3 entries. */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(application = Application::class, sdk = [35], qualifiers = "en-w673dp-h951dp-mdpi")
class FoldableWindowShotsTest {
    @get:Rule val rule = createEmptyComposeRule()
    @After fun resetAssistant() = AssistantManagerChoice.reset()
    private val agent = Session("computer", "agent", title = "Plan a weekend trip", state = "unread",
        cwd = "/example/travel", updatedAt = "2026-11-12T09:00:00Z")
    private val route = ChatRoute(agent.machineId, agent.sessionId, ConversationKind.Agent)
    private val otherAgent = agent.copy(sessionId = "packing", title = "Review the packing list")
    private val repositories = object : SessionsRepository {
        override val machines = MutableStateFlow(listOf(Machine("computer", "Computer A", online = true)))
        override val sessions = MutableStateFlow(listOf(agent, otherAgent))
        override suspend fun refresh() = Unit
    }
    private val interactions = object : InteractionCenter {
        override val pending = MutableStateFlow(emptyList<Interaction>())
        override suspend fun refresh() = Unit
        override suspend fun resolve(id: String, resolution: Resolution) = Unit
    }
    private val sender = object : MessageSender {
        override val outbox = MutableStateFlow(emptyList<OutboxMessage>())
        override suspend fun send(session: SessionKey, text: String, clientMessageId: String, clearDraft: String?) = Unit
    }
    private val chats = object : ChatRepository {
        override fun observe(session: SessionKey) = MutableStateFlow(ChatSnapshot(loading = false, items = listOf(
            Item("answer", 1, role = "assistant", kind = "text",
                text = if (session == otherAgent.key) "The packing list is ready." else "The trip outline is ready.", ts = "2026-11-12T09:00:00Z"),
        )))
        override suspend fun loadOlder(session: SessionKey) = Unit
        override suspend fun expandFold(session: SessionKey, fromSeq: Int, toSeq: Int) = emptyList<Item>()
    }
    private val connection = object : HubConnection {
        override val state = MutableStateFlow(ConnectionState.Online)
        override val events = MutableSharedFlow<ServerEvent>()
        override val lastHello = Hello(serverVersion = "1.0", protocol = 1)
        override fun reconnect() = Unit
        override fun send(message: JsonObject) = true
    }
    private fun preferences(): AgentsPreferences {
        val store = object : DataStore<Preferences> {
            override val data = MutableStateFlow<Preferences>(emptyPreferences())
            override suspend fun updateData(transform: suspend (Preferences) -> Preferences) = transform(data.value).also { data.value = it }
        }
        val hub = object : HubPreferences {
            override val preferences = MutableStateFlow(HubPreferencesSnapshot())
            override suspend fun refresh() = Unit
            override suspend fun togglePin(path: String, space: Boolean) = Unit
            override suspend fun toggleMute(path: String, space: Boolean) = Unit
            override suspend fun markSeen(session: SessionKey, version: String) = Unit
        }
        return AgentsPreferences(store, hub)
    }

    @Test fun uprightInnerScreen() = windows("upright")
    @Test fun uprightInnerScreenLarge() = windows("upright-large", large = true)
    @Config(qualifiers = "en-w951dp-h673dp-mdpi")
    @Test fun landscapeInnerScreen() = windows("landscape")
    @Config(qualifiers = "en-w411dp-h797dp-mdpi")
    @Test fun foldedOuterScreen() = windows("folded")
    @Config(qualifiers = "en-w1000dp-h800dp-mdpi")
    @Test fun wideWindow() = windows("wide")
    @Config(qualifiers = "en-w1000dp-h800dp-mdpi")
    @Test fun wideWindowAtMaximumDivider() = windows("wide-max", dragMax = true)

    private fun windows(window: String, large: Boolean = false, dragMax: Boolean = false) {
        AssistantManagerChoice.choose("hub", null, "assistant")
        val app = ApplicationProvider.getApplicationContext<Application>()
        Shadows.shadowOf(app.packageManager).addActivityIfNotPresent(ComponentName(app, ComponentActivity::class.java))
        val prefs = preferences()
        val dividerStore = if (dragMax) PaneSplitStore(app) else null
        val dependencies = object : AgentsDependencies {
            override fun sessions() = repositories
            override fun interactions() = this@FoldableWindowShotsTest.interactions
            override fun connection() = this@FoldableWindowShotsTest.connection
            override fun sender() = this@FoldableWindowShotsTest.sender
            override fun preferences() = prefs
            override fun machines(): MachineManagement? = null
        }
        val main = Session("computer", "assistant", title = "Main conversation", assistant = true)
        val recent = main.copy(sessionId = "recent", title = "Weekend plans", updatedAt = "2026-11-12T09:00:00Z")
        val snapshot = AssistantSnapshot(mainSession = main.key, conversations = listOf(main, recent))
        val assistantRepository = object : AssistantRepository {
            override val assistant = MutableStateFlow(snapshot)
            override suspend fun refresh() = Unit
        }
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        var selected by mutableStateOf(AppTab.Persona)
        val columnShown = mutableStateOf(true)
        val sidebarOverShown = mutableStateOf(false)
        val agentsOverShown = mutableStateOf(false)
        val drawerShown = mutableStateOf(false)
        val stacks = AppTab.entries.associateWith { mutableStateListOf<NavKey>(tabRoot(it)) }
        val navigator = AppNavigator(stacks, { selected }, { selected = it }, chatTab = { AppTab.Agents })
        val listNavigator = object : Navigator by navigator {
            override fun navigate(key: NavKey) { pickAgentsList(navigator, stacks.getValue(AppTab.Agents), agentsOverShown, key) }
        }
        rule.mainClock.autoAdvance = false
        ActivityScenario.launch(ComponentActivity::class.java).use { scenario ->
            scenario.onActivity { activity -> activity.setContent {
              CompositionLocalProvider(LocalDensity provides Density(LocalDensity.current.density, if (large) 2f else 1f)) {
                PersonaTheme(style = style, darkTheme = dark, reduceMotion = true) {
                    val scope = rememberCoroutineScope()
                    val width = with(LocalDensity.current) { LocalWindowInfo.current.containerSize.width.toDp() }
                    val height = with(LocalDensity.current) { LocalWindowInfo.current.containerSize.height.toDp() }
                    val regular = isRegularWindow(width.value, height.value)
                    val over = regular && columnPlacement(width.value, emptyList()) == ColumnPlacement.Over
                    val shown = if (over) sidebarOverShown else columnShown
                    val sidebar: @Composable () -> Unit = {
                        AssistantSidebarContent(snapshot, assistantRepository, main.key, scope, demo = true,
                            onOpen = { sidebarOverShown.value = false; drawerShown.value = false },
                            onAction = { sidebarOverShown.value = false; drawerShown.value = false },
                            onSettings = { sidebarOverShown.value = false; drawerShown.value = false },
                            onInbox = { sidebarOverShown.value = false; drawerShown.value = false },
                            onKnowledge = {}, onTodos = null, todoCount = 0, showTodos = false, inboxCount = 0,
                            settingsCount = 0, settingsDot = false, settingsBadge = null, pages = emptyList(), onPage = {}, onRetired = {})
                    }
                    val rootKeys = remember { listOf<NavKey>(AssistantRoute, AgentsRoute).map { NavEntry(it) {}.contentKey }.toSet() }
                    val assistantKey = remember { NavEntry<NavKey>(AssistantRoute) {}.contentKey }
                    val chrome = NavEntryDecorator<NavKey> { entry ->
                        val primary = entry.contentKey in rootKeys
                        val page: @Composable () -> Unit = {
                            CompositionLocalProvider(LocalHomeChrome provides if (primary) HomeChrome(navigationItems(emptyMap()), selected.name,
                                onSelect = { navigator.selectTab(AppTab.valueOf(it)) }, barHeight = remember { mutableIntStateOf(0) },
                                leading = { JPersonaButton(PersonaState.Idle, "Sidebar", {
                                    if (entry.contentKey == assistantKey && regular) shown.value = !shown.value else drawerShown.value = true
                                }) }) else null) { entry.Content() }
                        }
                        if (entry.contentKey == assistantKey) {
                            val split = sidebarSplit(width.value, emptyList())
                            JSidebarColumn(if (regular) sidebar else null, width = (if (over) PaneSplits.DefaultLeft else split.sidebar).dp,
                                pageStart = split.page.dp, shown = shown.value, overlay = over,
                                onDismiss = { sidebarOverShown.value = false }) { page() }
                        } else page()
                    }
                    val adaptive = currentWindowAdaptiveInfo()
                    val directive = calculatePaneScaffoldDirectiveWithTwoPanesOnMediumWidth(adaptive)
                    val stack = stacks.getValue(selected)
                    val listDetail = rememberShellListDetail(selected, directive, width, emptyList(), detailOf(stack) != null,
                        store = dividerStore, collapsible = canCollapseList(stack))
                    val overlay = rememberAgentsOverlayStrategy(over && selected == AppTab.Agents, agentsOverShown) {
                        CompositionLocalProvider(LocalHomeChrome provides HomeChrome(navigationItems(emptyMap()), selected.name,
                            onSelect = { navigator.selectTab(AppTab.valueOf(it)) }, leading = {
                                JPersonaButton(PersonaState.Idle, "Sidebar", { drawerShown.value = true })
                            })) {
                            AgentsEmptyPage { agentsOverShown.value = true }
                        }
                    }
                    Box(Modifier.fillMaxSize().background(PersonaTheme.palette.background).testTag("shot")) {
                        JShellDrawer(drawerShown.value, onDismiss = { drawerShown.value = false }, drawer = sidebar) {
                            NavDisplay(backStack = stack, onBack = navigator::back,
                                entryDecorators = listOf(rememberSaveableStateHolderNavEntryDecorator(), rememberViewModelStoreNavEntryDecorator(), chrome),
                                sceneStrategies = listOf(overlay, listDetail),
                                transitionSpec = { ContentTransform(EnterTransition.None, ExitTransition.None) },
                                popTransitionSpec = { ContentTransform(EnterTransition.None, ExitTransition.None) },
                                entryProvider = { key ->
                                    val provider = entryProvider<NavKey> {
                                        entry<AssistantRoute> {
                                            JPage("Assistant") { padding ->
                                                Box(Modifier.fillMaxSize().padding(padding).testTag("assistant-page")) {
                                                    ChatScreen(main.key, chats, sender, interactions, repositories, onBack = {}, embedded = true,
                                                        showTopBar = false, renderStyle = persona.feature.chat.ChatRenderStyle.Assistant, historySession = main)
                                                }
                                            }
                                        }
                                        agentsEntries(navigator, dependencies, listNavigator = listNavigator)
                                        entry<ChatRoute> { route ->
                                            Box(Modifier.fillMaxSize().testTag("agent-page")) {
                                                ChatScreen(SessionKey(route.machineId, route.sessionId), chats, sender, interactions, repositories, onBack = navigator::back)
                                            }
                                        }
                                    }
                                    val entry = provider(key)
                                    val metadata = when (key) {
                                        AgentsRoute -> ListDetailSceneStrategy.listPane(detailPlaceholder = { PageBackdrop() })
                                        is ChatRoute -> ListDetailSceneStrategy.detailPane()
                                        else -> emptyMap()
                                    }
                                    NavEntry(key, entry.contentKey, entry.metadata + metadata) { entry.Content() }
                                })
                        }
                    }
                }
              }
            } }
            for (s in AppStyle.entries) {
                var light: Bitmap? = null
                for (d in listOf(false, true)) {
                    rule.runOnIdle { style = s; dark = d; selected = AppTab.Persona; sidebarOverShown.value = false }
                    frames()
                    capture("$window-assistant-away", s, d)
                    val page = rule.onNodeWithTag("assistant-page").getBoundsInRoot()
                    if (window.startsWith("upright")) {
                        assertEquals(0f, page.left.value, 0.01f)
                        rule.onNodeWithContentDescription("Sidebar").performClick()
                        frames()
                        assertTrue(sidebarOverShown.value)
                        val open = capture("$window-assistant-over", s, d)
                        if (!d) light = open else assertTrue("light and dark must differ", !open.sameAs(light!!))
                        rule.onNodeWithContentDescription("Close").performTouchInput { click(centerRight) }
                        frames()
                        assertTrue(!sidebarOverShown.value)
                        rule.onNodeWithContentDescription("Sidebar").performClick()
                        frames()
                        rule.onNodeWithText("Weekend plans").performScrollTo().performClick()
                        frames()
                        assertTrue("a pick dismisses the sidebar", !sidebarOverShown.value)
                        capture("$window-assistant-picked", s, d)
                    }
                    rule.runOnIdle { navigator.selectTab(AppTab.Agents) }
                    if (window.startsWith("upright")) {
                        frames()
                        capture("$window-agents-empty-away", s, d)
                        rule.onNodeWithContentDescription("Show list").performClick()
                        awaitText(agent.displayTitle)
                        capture("$window-agents-empty-over", s, d)
                    }
                    awaitText(agent.displayTitle)
                    rule.onNodeWithText(agent.displayTitle, useUnmergedTree = true).performClick()
                    awaitText("The trip outline is ready.")
                    if (window.startsWith("upright")) {
                        val full = rule.onNodeWithTag("agent-page").getBoundsInRoot()
                        assertEquals(0f, full.left.value, 0.01f)
                        assertEquals(673f, (full.right - full.left).value, 0.01f)
                        capture("$window-agents-away", s, d)
                        rule.onNodeWithContentDescription("Sidebar").performClick()
                        frames()
                        assertTrue(agentsOverShown.value)
                        capture("$window-agents-over", s, d)
                        rule.onNodeWithContentDescription("Close").performTouchInput { click(centerRight) }
                        frames()
                        assertTrue(!agentsOverShown.value)
                        rule.onNodeWithContentDescription("Sidebar").performClick()
                        frames()
                        rule.onNodeWithText(agent.displayTitle).performClick()
                        frames()
                        assertTrue("reselecting the current row dismisses the list", !agentsOverShown.value)
                        capture("$window-agents-picked", s, d)
                        rule.onNodeWithContentDescription("Sidebar").performClick()
                        frames()
                        rule.onNodeWithText(otherAgent.displayTitle, useUnmergedTree = true).performClick()
                        awaitText("The packing list is ready.")
                        assertTrue(!agentsOverShown.value)
                        assertEquals("a list pick replaces the root detail", 2, stacks.getValue(AppTab.Agents).size)
                        rule.onNodeWithContentDescription("Sidebar").performClick()
                        frames()
                        assertTrue("the second picked detail can reopen its list", agentsOverShown.value)
                        rule.onNodeWithContentDescription("Close").performTouchInput { click(centerRight) }
                        frames()
                    } else {
                        if (dragMax) {
                            val resize = app.getString(persona.core.designsystem.R.string.designsystem_pane_resize)
                            repeat(3) {
                                rule.onNodeWithContentDescription(resize).performTouchInput {
                                    swipe(center, center + androidx.compose.ui.geometry.Offset(300f, 0f), 400)
                                }
                                repeat(100) { rule.mainClock.advanceTimeByFrame() }
                            }
                            frames()
                            // The 400dp placement token must not shorten the old 1000 - 320dp drag limit.
                            assertEquals(680f, dividerStore!!.share(AppTab.Agents)!! * 1000f, 0.01f)
                        }
                        capture("$window-agents-detail", s, d)
                    }
                }
            }
        }
    }
    private fun frames() {
        Snapshot.sendApplyNotifications()
        repeat(16) { rule.mainClock.advanceTimeByFrame() }
        rule.waitForIdle()
    }
    private fun awaitText(text: String) {
        val deadline = System.nanoTime() + 30_000_000_000L
        do {
            frames()
            if (rule.onAllNodesWithText(text, useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()) return
            Thread.sleep(20)
        } while (System.nanoTime() < deadline)
        error("Missing $text: ${rule.onRoot().printToString()}")
    }
    private fun capture(stage: String, style: AppStyle, dark: Boolean): Bitmap {
        frames()
        val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
        val out = File("build/foldable-window-shots").also { it.mkdirs() }
        File(out, "$stage-${style.name.lowercase()}-${if (dark) "dark" else "light"}.png").outputStream()
            .use { assertTrue(bitmap.compress(Bitmap.CompressFormat.PNG, 100, it)) }
        return bitmap
    }
}
