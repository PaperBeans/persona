package persona.feature.chat

import android.content.res.Configuration
import android.graphics.Bitmap
import android.view.View
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.getUnclippedBoundsInRoot
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.text.input.TextFieldValue
import androidx.compose.ui.unit.Density
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.height
import androidx.core.graphics.Insets
import androidx.core.view.ViewCompat
import androidx.core.view.WindowInsetsCompat
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.flowOf
import kotlinx.serialization.json.JsonObject
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.AskOption
import persona.core.contracts.FeedItem
import persona.core.contracts.FeedResponse
import persona.core.contracts.Interaction
import persona.core.contracts.Item
import persona.core.contracts.JsonCodec
import persona.core.contracts.Machine
import persona.core.contracts.QueueEdit
import persona.core.contracts.Resolution
import persona.core.contracts.ServerEvent
import persona.core.contracts.Session
import persona.core.contracts.SessionKey
import persona.core.contracts.Supervision
import persona.core.contracts.TaskPlan
import persona.core.contracts.Todo
import persona.core.contracts.TodoAgent
import persona.core.contracts.TodoAsk
import persona.core.contracts.api.AssistantConfig
import persona.core.contracts.api.AssistantManagement
import persona.core.contracts.api.AssistantNotes
import persona.core.contracts.api.ChatAction
import persona.core.contracts.api.ChatActionResult
import persona.core.contracts.api.ChatActions
import persona.core.contracts.api.ChatRepository
import persona.core.contracts.api.ChatSnapshot
import persona.core.contracts.api.ConnectionState
import persona.core.contracts.api.DraftUploadState
import persona.core.contracts.api.HubConnection
import persona.core.contracts.api.InteractionCenter
import persona.core.contracts.api.MemoryFile
import persona.core.contracts.api.MessageSender
import persona.core.contracts.api.OutboxMessage
import persona.core.contracts.api.Run
import persona.core.contracts.api.ScheduledTask
import persona.core.contracts.api.SessionsRepository
import persona.core.contracts.api.TasksApi
import persona.core.designsystem.AppStyle
import persona.core.designsystem.AssistantTrayTokens
import persona.core.designsystem.ChromeInsets
import persona.core.designsystem.JPage
import persona.core.designsystem.LocalChromeInsets
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.ScrollEdgeStyle
import persona.core.designsystem.Tokens
import persona.core.domain.tasks.TasksStore
import java.io.File
import java.time.Instant
import kotlin.math.roundToInt

/**
 * Verifies that an expanded Assistant tray (e.g. Needs you with a tall decision card) compresses and scrolls
 * internally—or collapses cleanly when space runs out—so [ChatComposer]'s input field and bottom toolbar remain
 * completely visible above the IME even when the route chip, image attachment preview, and launcher/permission
 * chips are shown together.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h840dp-xhdpi")
class AssistantTrayImeVisibilityTest {
    @get:Rule val rule = createComposeRule()
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)

    @After
    fun tearDown() {
        scope.cancel()
    }

    private val main = SessionKey("computer-a", "main")
    private val recentIso = Instant.now().minusSeconds(1800).toString()
    private val release = Session(
        "computer-a", "release", title = "Prepare the release", cwd = "/work/app",
        state = "needs_you", status = "blocked", attentionReason = "Needs your decision", updatedAt = recentIso,
    )
    private val worker = Session(
        "computer-a", "worker", title = "Verify migration scripts", cwd = "/work/app",
        state = "working", status = "working", activity = "Running integration tests", updatedAt = recentIso,
    )
    private val decision = Todo(
        "task-release", release.title, origin = "main", createdAt = recentIso, updatedAt = recentIso,
        agents = listOf(TodoAgent(release.machineId, release.sessionId, cwd = release.cwd)),
        supervision = Supervision(state = "needs_user"),
        ask = TodoAsk(
            outcome = "decide",
            verdict = "Needs your decision: release 2.4 to everyone now?",
            recommend = "Release now",
            askedAt = recentIso,
            question = "Release 2.4 to everyone now, or wait for the crash fix?",
            whyOwner = "irreversible",
            background = "From your earlier message. The build passed unit tests and awaits rollout confirmation.",
            options = listOf(
                AskOption("Release now", "Everyone gets 2.4 today; the crash fix follows in 2.4.1"),
                AskOption("Wait for the crash fix", "2.4 goes out with the fix, about two days later"),
                AskOption("Release to 10% first", "A staged rollout; widen it once the crash rate is known"),
            ),
            costOfWaiting = "The importer fix in 2.4 stays unreleased",
            default = "never",
            responses = listOf("decide_yourself", "not_what_i_asked", "explain_first"),
        ),
    )
    private val secondDecision = Todo(
        "task-docs", "Publish release notes", origin = "main", createdAt = recentIso, updatedAt = recentIso,
        supervision = Supervision(state = "needs_user"),
        ask = TodoAsk(
            outcome = "decide",
            verdict = "Publish the changelog alongside the release?",
            recommend = "Publish together",
            askedAt = recentIso,
            question = "Publish the changelog alongside the release?",
            options = listOf(
                AskOption("Publish together", "Post the changelog right after the build goes live"),
                AskOption("Hold for review", "Keep the draft ready until after rollout"),
            ),
        ),
    )
    private val runningTodo = Todo(
        "task-worker", worker.title, origin = "main", createdAt = recentIso, updatedAt = recentIso,
        agents = listOf(TodoAgent(worker.machineId, worker.sessionId, cwd = worker.cwd)),
    )
    private val personaSession = JsonCodec.decodeFromString<Session>(
        """{"machine_id":"computer-a","session_id":"main","title":"Persona","assistant":true,"agent":"claude",
            "launcher":"agy","permission_mode":"bypassPermissions","effort":"medium","status":"idle","state":"idle"}""",
    )
    private val samplePhoto = ComposerAttachment(
        id = "photo-1",
        name = "screen.jpg",
        uri = "",
        image = true,
        path = "/tmp/screen.jpg",
        uploadState = DraftUploadState.Uploaded,
    )

    private val api = object : TasksApi {
        override suspend fun todos() = listOf(decision, secondDecision, runningTodo)
        override suspend fun feed(conversation: String, since: String?) = FeedResponse(emptyList())
        override suspend fun startPlan(id: String, remember: Boolean): TaskPlan = error("not used")
        override suspend fun dismissPlan(id: String): TaskPlan = error("not used")
        override suspend fun retryTask(todoId: String): Todo = error("not used")
        override suspend fun removeTask(todoId: String) = Unit
        override suspend fun route(conversation: String, todoId: String, text: String): FeedItem = error("not used")
        override suspend fun today(conversation: String): FeedItem = error("not used")
        override suspend fun mainConversation() = "main"
        override suspend fun inboxReply(itemId: String, text: String, card: String?) = Unit
    }
    private val connection = object : HubConnection {
        override val state = MutableStateFlow(ConnectionState.Online)
        override val events = MutableSharedFlow<ServerEvent>()
        override fun reconnect() = Unit
        override fun send(message: JsonObject) = true
    }
    private val sessions = object : SessionsRepository {
        override val sessions = MutableStateFlow(listOf(personaSession, release, worker))
        override val machines = MutableStateFlow(listOf(Machine("computer-a", "Computer A", online = true)))
        override suspend fun refresh() = Unit
    }
    private val interactions = object : InteractionCenter {
        override val pending = MutableStateFlow(emptyList<Interaction>())
        override suspend fun refresh() = Unit
        override suspend fun resolve(id: String, resolution: Resolution) = Unit
    }
    private val chats = object : ChatRepository {
        override fun observe(session: SessionKey) = flowOf(
            ChatSnapshot(
                loading = false,
                items = listOf(
                    Item("u1", 1, role = "user", kind = "text", text = "Please check the release checklist."),
                    Item("a1", 2, role = "assistant", kind = "text", text = "I queued the decision in Needs you so you can choose how to roll out."),
                ),
            ),
        )
        override suspend fun loadOlder(session: SessionKey) = Unit
        override suspend fun expandFold(session: SessionKey, fromSeq: Int, toSeq: Int) = emptyList<Item>()
    }
    private val sender = object : MessageSender {
        override val outbox = MutableStateFlow(emptyList<OutboxMessage>())
        override suspend fun send(session: SessionKey, text: String, clientMessageId: String, clearDraft: String?) = Unit
    }
    private val actions = object : ChatActions {
        override suspend fun isMuted(session: Session) = false
        override suspend fun execute(session: Session, action: ChatAction): ChatActionResult = ChatActionResult.Done
        override suspend fun verify(session: Session, action: ChatAction): ChatActionResult = ChatActionResult.Done
        override suspend fun editQueue(session: Session, keep: List<Int>): QueueEdit = QueueEdit(session.queued.orEmpty())
        override suspend fun sendQueuedNow(session: Session) = Unit
    }
    private val management = object : AssistantManagement {
        override suspend fun config() = AssistantConfig(main = "main", launcher = "agy")
        override suspend fun tasks() = emptyList<ScheduledTask>()
        override suspend fun warm() = Unit
        override suspend fun endConversation(session: SessionKey) = Unit
        override suspend fun saveTask(task: ScheduledTask) = task
        override suspend fun deleteTask(id: String) = Unit
        override suspend fun runTask(id: String) = main
        override suspend fun freshTask(id: String) = ScheduledTask(id)
        override suspend fun runs() = emptyList<Run>()
        override suspend fun notes() = AssistantNotes()
        override suspend fun setNotes(text: String) = Unit
        override suspend fun memory() = emptyList<MemoryFile>()
        override suspend fun saveMemory(name: String, content: String) = Unit
        override suspend fun deleteMemory(name: String) = Unit
    }

    private fun settle() {
        Snapshot.sendApplyNotifications()
        repeat(6) { rule.mainClock.advanceTimeByFrame() }
        rule.mainClock.advanceTimeBy(300)
        rule.waitForIdle()
    }

    private fun dpToPx(view: View, dp: Int): Int = (dp * view.resources.displayMetrics.density).roundToInt()

    private fun dispatchIme(view: View, imeDp: Int, navDp: Int = 48) {
        rule.runOnUiThread {
            val insets = WindowInsetsCompat.Builder()
                .setInsets(WindowInsetsCompat.Type.navigationBars(), Insets.of(0, 0, 0, dpToPx(view, navDp)))
                .setVisible(WindowInsetsCompat.Type.navigationBars(), navDp > 0)
                .setInsets(WindowInsetsCompat.Type.ime(), Insets.of(0, 0, 0, dpToPx(view, imeDp)))
                .setVisible(WindowInsetsCompat.Type.ime(), imeDp > 0)
                .build()
            ViewCompat.dispatchApplyWindowInsets(view, insets)
        }
        settle()
    }

    private fun saveShot(name: String) {
        val output = File(System.getProperty("persona.shots") ?: "build/tray-ime-composer-shots").also { it.mkdirs() }
        val bitmap = rule.onNodeWithTag("tray-ime-shot").captureToImage().asAndroidBitmap()
        File(output, "$name.png").outputStream().use {
            assertTrue(bitmap.compress(Bitmap.CompressFormat.PNG, 100, it))
        }
    }

    @Test
    fun trayBudgetDeductsHeaderInsetsFloorAndOccupiedFooterChrome() {
        // Plentiful height keeps the existing 330dp / 45% upper caps unchanged.
        assertEquals(
            AssistantTrayTokens.maximumHeight,
            assistantTrayMaxHeight(page = 900.dp, header = 96.dp, occupied = 180.dp, bottomInset = 24.dp, floor = 24.dp),
        )
        assertEquals(
            270.dp,
            assistantTrayMaxHeight(page = 600.dp, header = 60.dp, occupied = 120.dp, bottomInset = 0.dp, floor = 24.dp),
        )
        // IME open: remaining room after header + composer/route/attachments/bar wins over the 45% share (225dp).
        assertEquals(
            84.dp,
            assistantTrayMaxHeight(page = 500.dp, header = 96.dp, occupied = 320.dp, bottomInset = 0.dp, floor = 0.dp),
        )
        // Floor and bottomInset are also deducted when present.
        assertEquals(
            40.dp,
            assistantTrayMaxHeight(page = 500.dp, header = 96.dp, occupied = 320.dp, bottomInset = 20.dp, floor = 24.dp),
        )
        // Exhausted room clamps cleanly to 0dp, never negative.
        assertEquals(
            0.dp,
            assistantTrayMaxHeight(page = 360.dp, header = 96.dp, occupied = 320.dp, bottomInset = 0.dp, floor = 0.dp),
        )
    }

    @Test
    fun expandedNeedsYouTrayWithRouteAttachmentChipsAndImeKeepsComposerVisibleAcrossStyles() {
        val tasks = TasksStore(api, connection, scope).also { it.reloadTodos() }
        tasks.setRoute("main", decision.id)
        var style by mutableStateOf(AppStyle.Brief)
        var dark by mutableStateOf(false)
        val composer = ChatComposerState().apply {
            focused = true
            value = TextFieldValue("")
            attachments = listOf(samplePhoto)
        }
        lateinit var hostView: View
        rule.mainClock.autoAdvance = false
        rule.setContent {
            hostView = LocalView.current
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                Box(
                    Modifier.width(400.dp).height(840.dp)
                        .background(PersonaTheme.palette.background)
                        .testTag("tray-ime-shot"),
                ) {
                    key(style, dark) {
                        // Mirrors AssistantScreen hosting embedded ChatScreen with an outer topBar + imePadding().
                        JPage(
                            title = "Persona",
                            modifier = Modifier.fillMaxSize().imePadding(),
                            topBar = { Box(Modifier.fillMaxWidth().height(72.dp).testTag("host-top-bar")) },
                            edges = false,
                            scrollEdgeStyle = ScrollEdgeStyle.Gradient,
                        ) { padding ->
                            val host = ChromeInsets(
                                bottom = padding.calculateBottomPadding(),
                                handlesNavigationBar = true,
                                top = padding.calculateTopPadding(),
                            )
                            CompositionLocalProvider(LocalChromeInsets provides host) {
                                ChatScreen(
                                    main, chats, sender, interactions, sessions, {},
                                    chrome = ChatChromeBindings(
                                        actions = actions,
                                        connection = connection,
                                        tasks = tasks,
                                        assistantManagement = management,
                                        onOpenSession = {},
                                    ),
                                    renderStyle = ChatRenderStyle.Assistant,
                                    embedded = true,
                                    showTopBar = false,
                                    composerState = composer,
                                )
                            }
                        }
                    }
                }
            }
        }
        settle()
        rule.waitUntil(10_000) { rule.onAllNodesWithTag("assistant-entry-needs").fetchSemanticsNodes().isNotEmpty() }

        val imeHeightDp = 340
        for (currentStyle in AppStyle.entries) for (isDark in listOf(false, true)) {
            style = currentStyle
            dark = isDark
            settle()
            dispatchIme(hostView, imeDp = 0)
            // Expand Needs you tray while keyboard is down, then bring up the IME in steps.
            rule.onNodeWithTag("assistant-entry-needs").performClick()
            settle()
            rule.onNodeWithTag("assistant-tray-needs").assertIsDisplayed()
            rule.onNode(hasSetTextAction()).performClick()
            for (step in listOf(100, 220, imeHeightDp)) dispatchIme(hostView, imeDp = step)

            val root = rule.onNodeWithTag("tray-ime-shot").getUnclippedBoundsInRoot()
            val topBar = rule.onNodeWithTag("host-top-bar").getUnclippedBoundsInRoot()
            val visibleBottom = root.bottom - imeHeightDp.dp
            val trayCard = rule.onNodeWithTag("assistant-tray-needs").getUnclippedBoundsInRoot()
            val trayBar = rule.onNodeWithTag("assistant-tray-bar").getUnclippedBoundsInRoot()
            val routeChip = rule.onNodeWithTag("assistant-route-chip").getUnclippedBoundsInRoot()
            val photo = rule.onNodeWithContentDescription("Photo 1").getUnclippedBoundsInRoot()
            val field = rule.onNode(hasSetTextAction()).getUnclippedBoundsInRoot()
            val add = rule.onNodeWithContentDescription("Add attachments and quick messages").getUnclippedBoundsInRoot()
            val label = "$currentStyle dark=$isDark"

            rule.onNodeWithTag("assistant-tray-needs").assertIsDisplayed()
            rule.onNodeWithTag("assistant-route-chip").assertIsDisplayed()
            rule.onNodeWithContentDescription("Photo 1").assertIsDisplayed()
            rule.onNode(hasSetTextAction()).assertIsDisplayed()
            rule.onNodeWithContentDescription("Add attachments and quick messages").assertIsDisplayed()

            assertTrue("$label: expanded tray card stays below top bar (${trayCard.top} >= ${topBar.bottom})", trayCard.top >= topBar.bottom - 1.dp)
            assertTrue("$label: expanded tray card sits above tray capsule bar (${trayCard.bottom} <= ${trayBar.top})", trayCard.bottom <= trayBar.top + 1.dp)
            assertTrue("$label: tray capsule bar sits above route chip (${trayBar.bottom} <= ${routeChip.top})", trayBar.bottom <= routeChip.top + 1.dp)
            assertTrue("$label: route chip sits above photo preview (${routeChip.bottom} <= ${photo.top})", routeChip.bottom <= photo.top + 1.dp)
            assertTrue("$label: photo preview sits above input field (${photo.bottom} <= ${field.top})", photo.bottom <= field.top + 1.dp)
            assertTrue("$label: input field has usable height (${field.height})", field.height >= Tokens.Space.l)
            assertTrue("$label: input field sits above bottom toolbar (${field.bottom} <= ${add.top})", field.bottom <= add.top + 1.dp)
            assertTrue("$label: bottom toolbar keeps 48dp touch height (${add.height})", add.height >= Tokens.MinTouch.android)
            assertTrue("$label: bottom toolbar stays inside visible area above IME (${add.bottom} <= $visibleBottom)", add.bottom <= visibleBottom + 1.dp)

            saveShot("tray-ime-${currentStyle.name.lowercase()}-${if (isDark) "dark" else "light"}")
        }
    }

    @Test
    fun addingRouteAndAttachmentWhileTrayIsExpandedUnderImeCompressesTrayImmediately() {
        val tasks = TasksStore(api, connection, scope).also { it.reloadTodos() }
        val composer = ChatComposerState().apply {
            focused = true
            value = TextFieldValue("")
        }
        lateinit var hostView: View
        rule.mainClock.autoAdvance = false
        rule.setContent {
            hostView = LocalView.current
            PersonaTheme(darkTheme = true, reduceMotion = true, style = AppStyle.Brief) {
                Box(Modifier.width(400.dp).height(840.dp).background(PersonaTheme.palette.background)) {
                    JPage(
                        title = "Persona",
                        modifier = Modifier.fillMaxSize().imePadding(),
                        topBar = { Box(Modifier.fillMaxWidth().height(72.dp).testTag("host-top-bar")) },
                        edges = false,
                        scrollEdgeStyle = ScrollEdgeStyle.Gradient,
                    ) { padding ->
                        val host = ChromeInsets(
                            bottom = padding.calculateBottomPadding(),
                            handlesNavigationBar = true,
                            top = padding.calculateTopPadding(),
                        )
                        CompositionLocalProvider(LocalChromeInsets provides host) {
                            ChatScreen(
                                main, chats, sender, interactions, sessions, {},
                                chrome = ChatChromeBindings(
                                    actions = actions,
                                    connection = connection,
                                    tasks = tasks,
                                    assistantManagement = management,
                                    onOpenSession = {},
                                ),
                                renderStyle = ChatRenderStyle.Assistant,
                                embedded = true,
                                showTopBar = false,
                                composerState = composer,
                            )
                        }
                    }
                }
            }
        }
        settle()
        rule.waitUntil(10_000) { rule.onAllNodesWithTag("assistant-entry-needs").fetchSemanticsNodes().isNotEmpty() }

        // Start with the IME already up and the Needs you tray already expanded with a bare composer.
        dispatchIme(hostView, imeDp = 340)
        rule.onNodeWithTag("assistant-entry-needs").performClick()
        settle()
        val trayBefore = rule.onNodeWithTag("assistant-tray-needs").getUnclippedBoundsInRoot()

        // Dynamically add both the route chip and an image attachment while IME + expanded tray stay open.
        rule.runOnUiThread {
            tasks.setRoute("main", decision.id)
            composer.attachments = listOf(samplePhoto)
        }
        settle()

        val visibleBottom = (840 - 340).dp
        val trayAfter = rule.onNodeWithTag("assistant-tray-needs").getUnclippedBoundsInRoot()
        val trayBar = rule.onNodeWithTag("assistant-tray-bar").getUnclippedBoundsInRoot()
        val routeChip = rule.onNodeWithTag("assistant-route-chip").getUnclippedBoundsInRoot()
        val field = rule.onNode(hasSetTextAction()).getUnclippedBoundsInRoot()
        val add = rule.onNodeWithContentDescription("Add attachments and quick messages").getUnclippedBoundsInRoot()

        assertTrue("expanded tray shrank immediately to make room (${trayAfter.height} < ${trayBefore.height})", trayAfter.height < trayBefore.height)
        assertTrue("tray card stays above tray bar", trayAfter.bottom <= trayBar.top + 1.dp)
        assertTrue("tray bar stays above route chip", trayBar.bottom <= routeChip.top + 1.dp)
        assertTrue("input field stays displayed above toolbar", field.bottom <= add.top + 1.dp)
        assertTrue("toolbar stays completely above IME (${add.bottom} <= $visibleBottom)", add.bottom <= visibleBottom + 1.dp)
        rule.onNode(hasSetTextAction()).assertIsDisplayed()
        rule.onNodeWithContentDescription("Add attachments and quick messages").assertIsDisplayed()
    }

    @Test
    fun extremeTightImeAndLargeTextKeepComposerVisibleAndRestoreExpandedTrayWhenImeCloses() {
        val tasks = TasksStore(api, connection, scope).also { it.reloadTodos() }
        tasks.setRoute("main", decision.id)
        var style by mutableStateOf(AppStyle.Glass)
        var fontScale by mutableStateOf(1f)
        val composer = ChatComposerState().apply {
            focused = true
            value = TextFieldValue("")
            attachments = listOf(samplePhoto)
        }
        lateinit var hostView: View
        rule.mainClock.autoAdvance = false
        rule.setContent {
            hostView = LocalView.current
            val baseDensity = LocalDensity.current
            val configuration = Configuration(LocalConfiguration.current).apply {
                this.fontScale = fontScale
            }
            CompositionLocalProvider(
                LocalConfiguration provides configuration,
                LocalDensity provides Density(baseDensity.density, fontScale),
            ) {
                PersonaTheme(darkTheme = true, reduceMotion = true, style = style) {
                    Box(
                        Modifier.width(400.dp).height(840.dp)
                            .background(PersonaTheme.palette.background)
                            .testTag("tray-ime-shot"),
                    ) {
                        key(style, fontScale) {
                            JPage(
                                title = "Persona",
                                modifier = Modifier.fillMaxSize().imePadding(),
                                topBar = { Box(Modifier.fillMaxWidth().height(72.dp).testTag("host-top-bar")) },
                                edges = false,
                                scrollEdgeStyle = ScrollEdgeStyle.Gradient,
                            ) { padding ->
                                val host = ChromeInsets(
                                    bottom = padding.calculateBottomPadding(),
                                    handlesNavigationBar = true,
                                    top = padding.calculateTopPadding(),
                                )
                                CompositionLocalProvider(LocalChromeInsets provides host) {
                                    ChatScreen(
                                        main, chats, sender, interactions, sessions, {},
                                        chrome = ChatChromeBindings(
                                            actions = actions,
                                            connection = connection,
                                            tasks = tasks,
                                            assistantManagement = management,
                                            onOpenSession = {},
                                        ),
                                        renderStyle = ChatRenderStyle.Assistant,
                                        embedded = true,
                                        showTopBar = false,
                                        composerState = composer,
                                    )
                                }
                            }
                        }
                    }
                }
            }
        }
        settle()
        rule.waitUntil(10_000) { rule.onAllNodesWithTag("assistant-entry-needs").fetchSemanticsNodes().isNotEmpty() }

        // Test both an extreme 460dp IME at 1.0x and a 340dp IME at 2.0x system font scale across all styles.
        for ((scale, imeDp, shotSuffix) in listOf(
            Triple(1f, 460, "extreme-ime"),
            Triple(2f, 340, "large-2x-ime"),
        )) {
            for (currentStyle in AppStyle.entries) {
                style = currentStyle
                fontScale = scale
                settle()
                dispatchIme(hostView, imeDp = 0)
                rule.onNodeWithTag("assistant-entry-needs").performClick()
                settle()
                rule.onNodeWithTag("assistant-tray-needs").assertIsDisplayed()

                dispatchIme(hostView, imeDp = imeDp)
                val visibleBottom = (840 - imeDp).dp
                val field = rule.onNode(hasSetTextAction()).getUnclippedBoundsInRoot()
                val add = rule.onNodeWithContentDescription("Add attachments and quick messages").getUnclippedBoundsInRoot()
                val label = "$currentStyle scale=$scale ime=$imeDp"

                rule.onNode(hasSetTextAction()).assertIsDisplayed()
                rule.onNodeWithContentDescription("Add attachments and quick messages").assertIsDisplayed()
                assertTrue("$label: input field stays below viewport top (${field.top} >= 0)", field.top >= 0.dp)
                assertTrue("$label: input field stays above bottom toolbar (${field.bottom} <= ${add.top})", field.bottom <= add.top + 1.dp)
                assertTrue("$label: bottom toolbar stays inside visible area above IME (${add.bottom} <= $visibleBottom)", add.bottom <= visibleBottom + 1.dp)
                // If the expanded tray card is drawn at all, it must be at least one touch-height row tall (never an empty sliver).
                val trayNodes = rule.onAllNodesWithTag("assistant-tray-needs").fetchSemanticsNodes()
                if (trayNodes.isNotEmpty() && trayNodes.first().size.height > 0) {
                    val trayBounds = rule.onNodeWithTag("assistant-tray-needs").getUnclippedBoundsInRoot()
                    assertTrue("$label: visible tray card avoids empty sliver (${trayBounds.height} >= ${Tokens.MinTouch.android})",
                        trayBounds.height >= Tokens.MinTouch.android)
                }
                saveShot("tray-$shotSuffix-${currentStyle.name.lowercase()}-dark")

                // Dismissing the IME restores the still-expanded tray card without requiring a second tap.
                dispatchIme(hostView, imeDp = 0)
                rule.onNodeWithTag("assistant-tray-needs").assertIsDisplayed()
            }
        }
    }

    @Test
    fun standaloneAssistantChatWithTopBarAndExpandedTrayKeepsComposerVisibleUnderIme() {
        val tasks = TasksStore(api, connection, scope).also { it.reloadTodos() }
        tasks.setRoute("main", decision.id)
        var style by mutableStateOf(AppStyle.Brief)
        val composer = ChatComposerState().apply {
            focused = true
            value = TextFieldValue("")
            attachments = listOf(samplePhoto)
        }
        lateinit var hostView: View
        rule.mainClock.autoAdvance = false
        rule.setContent {
            hostView = LocalView.current
            PersonaTheme(darkTheme = true, reduceMotion = true, style = style) {
                Box(Modifier.width(400.dp).height(840.dp).background(PersonaTheme.palette.background)) {
                    key(style) {
                        ChatScreen(
                            main, chats, sender, interactions, sessions, {},
                            chrome = ChatChromeBindings(
                                actions = actions,
                                connection = connection,
                                tasks = tasks,
                                assistantManagement = management,
                                onOpenSession = {},
                            ),
                            renderStyle = ChatRenderStyle.Assistant,
                            embedded = false,
                            showTopBar = true,
                            composerState = composer,
                        )
                    }
                }
            }
        }
        settle()
        rule.waitUntil(10_000) { rule.onAllNodesWithTag("assistant-entry-needs").fetchSemanticsNodes().isNotEmpty() }

        val imeDp = 340
        val visibleBottom = (840 - imeDp).dp
        for (currentStyle in AppStyle.entries) {
            style = currentStyle
            settle()
            dispatchIme(hostView, imeDp = 0)
            rule.onNodeWithTag("assistant-entry-needs").performClick()
            settle()
            rule.onNodeWithTag("assistant-tray-needs").assertIsDisplayed()

            dispatchIme(hostView, imeDp = imeDp)
            val field = rule.onNode(hasSetTextAction()).getUnclippedBoundsInRoot()
            val add = rule.onNodeWithContentDescription("Add attachments and quick messages").getUnclippedBoundsInRoot()
            rule.onNode(hasSetTextAction()).assertIsDisplayed()
            rule.onNodeWithContentDescription("Add attachments and quick messages").assertIsDisplayed()
            assertTrue("standalone $currentStyle: input above toolbar", field.bottom <= add.top + 1.dp)
            assertTrue("standalone $currentStyle: toolbar above IME (${add.bottom} <= $visibleBottom)", add.bottom <= visibleBottom + 1.dp)
        }
    }
}
