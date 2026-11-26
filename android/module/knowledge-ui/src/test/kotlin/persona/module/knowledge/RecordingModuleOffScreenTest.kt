package persona.module.knowledge

import android.graphics.Bitmap
import androidx.activity.ComponentActivity
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.unit.dp
import androidx.navigation3.runtime.NavKey
import androidx.navigation3.runtime.entryProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.HubScope
import persona.core.contracts.Machine
import persona.core.contracts.Session
import persona.core.contracts.SessionKey
import persona.core.contracts.api.*
import persona.core.designsystem.AppStyle
import persona.core.designsystem.LocalHubFeatures
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.Tokens
import persona.core.navigation.AppTab
import persona.core.navigation.KnowledgeDetailRoute
import persona.core.navigation.Navigator
import java.io.File

/**
 * A recording's page on a hub whose recording module is off (#15214): everything stored stays readable, the controls
 * that write through the module's routes are gone, a paused recording says so and is not read again every few seconds;
 * with the module on the page is as it was. The captures (each style, light and dark) go to `build/i18n-shots/` (or
 * `-Dpersona.shots=<dir>`) to look at.
 */
@OptIn(ExperimentalTestApi::class)
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h2400dp-xhdpi")
class RecordingModuleOffScreenTest {
    @get:Rule val rule = createAndroidComposeRule<ComponentActivity>()
    private val handToAgent: SemanticsMatcher = hasContentDescription("Hand to Agent")
    private val transcriptLine = "The widgets are ready, so Monday works for the release."
    private val pausedTitle = "Minutes are paused"

    @Test fun withTheModuleOnThePageOffersItsWritesAsBefore() {
        val recording = StoredMeeting()
        show(recording, recordingOn = true)
        rule.waitUntilExactlyOneExists(hasTestTag("minutes-make"), 10_000)
        rule.onNodeWithText("Regenerate").assertExists()
        rule.onAllNodesWithTag("minutes-add").assertCountEquals(2)
        rule.onNodeWithTag("minutes-add-all").assertExists()
        rule.onNodeWithTag("minutes-added").assertExists()
        rule.onNode(handToAgent).assertExists()
        rule.onAllNodesWithTag("minutes-paused").assertCountEquals(0)
        readable()
    }

    @Test fun withTheModuleOffEverythingStoredStaysAndTheWritesAreGone() {
        val recording = StoredMeeting()
        show(recording, recordingOn = false)
        rule.waitUntilExactlyOneExists(hasText(transcriptLine), 10_000)
        readable()
        // Gone, not greyed out: the recording module's routes answer 404 on this hub.
        rule.onAllNodesWithTag("minutes-make").assertCountEquals(0)
        rule.onAllNodesWithText("Regenerate").assertCountEquals(0)
        rule.onAllNodesWithTag("minutes-add").assertCountEquals(0)
        rule.onAllNodesWithTag("minutes-add-all").assertCountEquals(0)
        rule.onAllNodes(handToAgent).assertCountEquals(0)
        // Nothing was in flight, so nothing is paused.
        rule.onAllNodesWithTag("minutes-paused").assertCountEquals(0)
        assertTrue(recording.writes.isEmpty())
    }

    @Test fun aPausedRecordingSaysSoInPlaceOfWritingAndIsNotReadAgain() {
        val recording = StoredMeeting(state = "pending", paused = RecordingPaused.ModuleOff, minutes = null)
        show(recording, recordingOn = false)
        rule.waitUntilExactlyOneExists(hasTestTag("minutes-paused"), 10_000)
        rule.onNodeWithText(pausedTitle).assertExists()
        rule.onNodeWithText("Meeting recording is off on this hub, so nothing new is being written. Everything already here stays.").assertExists()
        // A status, not a control.
        rule.onNodeWithTag("minutes-paused").assertHasNoClickAction()
        rule.onAllNodesWithTag("minutes-writing").assertCountEquals(0)
        rule.onAllNodesWithText("Writing the minutes…").assertCountEquals(0)
        rule.onAllNodesWithTag("minutes-make").assertCountEquals(0)
        rule.onNodeWithText(transcriptLine).assertExists()
        rule.onNodeWithText("Ask Persona").assertExists()

        // The page would read a pending recording again every five seconds; a paused one it leaves alone.
        val reads = recording.reads
        rule.mainClock.advanceTimeBy(16_000)
        rule.waitForIdle()
        assertEquals(reads, recording.reads)
    }

    @Test fun pausedMinutesBeingRewrittenKeepTheStoredOnes() {
        show(StoredMeeting(state = "pending", paused = RecordingPaused.ModuleOff), recordingOn = false)
        rule.waitUntilExactlyOneExists(hasTestTag("minutes-paused"), 10_000)
        rule.onAllNodesWithText("Updating…").assertCountEquals(0)
        readable()
        rule.onAllNodesWithTag("minutes-add").assertCountEquals(0)
    }

    @Test fun pendingMinutesWithTheModuleOnAreStillWrittenAndReadAgain() {
        val recording = StoredMeeting(state = "pending", minutes = null)
        show(recording, recordingOn = true)
        rule.waitUntilExactlyOneExists(hasTestTag("minutes-writing"), 10_000)
        rule.onNodeWithText("Writing the minutes…").assertExists()
        rule.onAllNodesWithTag("minutes-paused").assertCountEquals(0)
        val reads = recording.reads
        rule.mainClock.advanceTimeBy(6_000)
        rule.waitUntil(10_000) { recording.reads > reads }
    }

    @Test fun aPauseReasonThisAppDoesNotKnowChangesNothing() {
        val recording = StoredMeeting(state = "pending", paused = RecordingPaused.Unknown, minutes = null)
        show(recording, recordingOn = true)
        rule.waitUntilExactlyOneExists(hasTestTag("minutes-writing"), 10_000)
        rule.onAllNodesWithText(pausedTitle).assertCountEquals(0)
        rule.onAllNodesWithTag("minutes-paused").assertCountEquals(0)
        rule.onNodeWithText(transcriptLine).assertExists()
        val reads = recording.reads
        rule.mainClock.advanceTimeBy(6_000)
        rule.waitUntil(10_000) { recording.reads > reads }
    }

    /** The page as the module registers it: the hub's hello decides, by the rule every module follows. */
    @Test fun thePageFollowsTheHubsHello() {
        var features by mutableStateOf(emptySet<String>())
        val connection = StoredMeeting().connection()
        rule.setContent {
            PersonaTheme(reduceMotion = true) {
                CompositionLocalProvider(LocalHubFeatures provides features) {
                    val provider = entryProvider<NavKey> {
                        knowledgeEntries(ModuleOffNavigator, connection, ModuleOffConversations, ModuleOffSender, { ModuleOffSender }, sessions = ModuleOffSessions(), openLinkInApp = {})
                    }
                    provider(KnowledgeDetailRoute(KnowledgeKind.Recordings, StoredMeeting.ID)).Content()
                }
            }
        }
        // No hello yet: on.
        rule.waitUntilExactlyOneExists(hasTestTag("minutes-make"), 10_000)
        for ((hello, on) in listOf(
            setOf("extensions") to true,
            setOf("modules", "module.knowledge-ui") to false,
            setOf("modules", "module.knowledge-ui", "module.recording") to true,
            setOf("module.knowledge-ui") to true,
        )) {
            rule.runOnIdle { features = hello }
            rule.waitForIdle()
            val count = if (on) 1 else 0
            rule.onAllNodesWithTag("minutes-make").assertCountEquals(count)
            rule.onAllNodes(handToAgent).assertCountEquals(count)
            rule.onAllNodesWithTag("minutes-add").assertCountEquals(count * 2)
            rule.onNodeWithText(transcriptLine).assertExists()
        }
    }

    @Test fun shotsEnglish() = shots("en")

    @Config(sdk = [35], qualifiers = "zh-w400dp-h2400dp-xhdpi")
    @Test fun shotsChinese() = shots("zh")

    @Config(sdk = [35], qualifiers = "ja-w400dp-h2400dp-xhdpi")
    @Test fun shotsJapanese() = shots("ja")

    /** Scenes: the module on (as before), off with stored minutes, off with rewritten minutes paused, off with none yet. */
    private fun shots(language: String) {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        var scene by mutableStateOf(0)
        val scenes = listOf(
            "on" to (StoredMeeting() to true),
            "off" to (StoredMeeting() to false),
            "off-paused" to (StoredMeeting(state = "pending", paused = RecordingPaused.ModuleOff) to false),
            "off-paused-none" to (StoredMeeting(state = "pending", paused = RecordingPaused.ModuleOff, minutes = null) to false),
        )
        val models = scenes.map { (_, made) -> model(made.first) }
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                Box(Modifier.testTag("shot").width(400.dp).height(2400.dp).background(PersonaTheme.palette.background)) {
                    androidx.compose.runtime.key(scene) {
                        KnowledgeDetailScreen(models[scene], ModuleOffNavigator, openLinkInApp = {}, recordingOn = scenes[scene].second.second)
                    }
                }
            }
        }
        val out = File(System.getProperty("persona.shots") ?: "build/i18n-shots").also { it.mkdirs() }
        for (index in scenes.indices) for (s in AppStyle.entries) for (d in listOf(false, true)) {
            rule.runOnIdle { scene = index; style = s; dark = d }
            rule.waitUntilAtLeastOneExists(hasText(transcriptLine), 10_000)
            rule.waitForIdle()
            val paused = rule.onAllNodesWithTag("minutes-paused").fetchSemanticsNodes().size
            assertEquals(scenes[index].first, if (index >= 2) 1 else 0, paused)
            val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
            val name = "recording-module-${scenes[index].first}-$language-${s.name.lowercase()}-${if (d) "dark" else "light"}.png"
            File(out, name).outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
        }
    }

    @Config(sdk = [35], qualifiers = "en-w360dp-h1300dp-xhdpi", fontScale = 2f)
    @Test fun pausedAtLargeText() = pausedStates("en-large")

    @Config(sdk = [35], qualifiers = "ja-w360dp-h900dp-xhdpi")
    @Test fun pausedOnANarrowScreenInJapanese() = pausedStates("ja-narrow")

    /** The minutes section alone, paused: none yet, and stored ones whose rewrite is paused (no Regenerate, no Add). */
    private fun pausedStates(name: String) {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        val actions = MinutesActions(busy = false, message = null, make = {}, add = {}, copy = {}, jump = {})
        val short = StoredMeeting.Minutes.copy(points = emptyList(), decisions = emptyList(), actions = StoredMeeting.Minutes.actions.take(2))
        val scenes = listOf(
            MinutesView(MinutesPhase.Paused, canAdd = false, paused = true),
            MinutesView(MinutesPhase.Ready, short, canAdd = false, paused = true),
        )
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                Column(Modifier.testTag("shot").width(360.dp).background(PersonaTheme.palette.background).padding(Tokens.Space.l)) {
                    for (view in scenes) LazyColumn(Modifier.fillMaxWidth().padding(bottom = Tokens.Space.l)) {
                        recordingMinutes(view, actions) { null }
                    }
                }
            }
        }
        val out = File(System.getProperty("persona.shots") ?: "build/i18n-shots").also { it.mkdirs() }
        for (s in AppStyle.entries) for (d in listOf(false, true)) {
            rule.runOnIdle { style = s; dark = d }
            rule.waitForIdle()
            rule.onAllNodesWithTag("minutes-paused").assertCountEquals(2)
            rule.onAllNodesWithTag("minutes-add").assertCountEquals(0)
            rule.onAllNodesWithTag("minutes-make").assertCountEquals(0)
            val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
            File(out, "recording-module-paused-states-$name-${s.name.lowercase()}-${if (d) "dark" else "light"}.png")
                .outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
        }
    }

    /** What the module being off must never take away. */
    private fun readable() {
        rule.onNodeWithText(transcriptLine).assertExists()
        rule.onNodeWithText("Ship the widgets on Monday.").assertExists()
        rule.onNodeWithText("Write the release notes").assertExists()
        rule.onNodeWithTag("minutes-added").assertExists()
        rule.onNodeWithTag("minutes-copy").assertExists()
        rule.onNodeWithTag("minutes-share").assertExists()
        rule.onAllNodesWithTag("minutes-time")[0].assertHasClickAction()
        // Kept: a chat message and the core's tagging route are not recording activity.
        rule.onNodeWithText("Ask Persona").assertIsEnabled()
        rule.onNode(hasContentDescription("Tag")).assertIsEnabled()
    }

    private fun model(recording: StoredMeeting) = KnowledgeDetailViewModel(KnowledgeKind.Recordings, StoredMeeting.ID,
        recording.connection(), ModuleOffSender, sessions = ModuleOffSessions())

    private fun show(recording: StoredMeeting, recordingOn: Boolean) {
        val model = model(recording)
        rule.setContent {
            PersonaTheme(reduceMotion = true) { KnowledgeDetailScreen(model, ModuleOffNavigator, openLinkInApp = {}, recordingOn = recordingOn) }
        }
    }
}

/** A finished weekly meeting as the hub keeps it: transcript, minutes (one action a to-do, two not), no hand-off yet. */
private class StoredMeeting(
    private val state: String = "ready",
    private val paused: RecordingPaused? = null,
    private val minutes: RecordingMinutes? = Minutes,
) : KnowledgeRepository {
    override val scope = HubScope("demo", "recording-module-off")
    var reads = 0
    val writes = mutableListOf<String>()

    fun connection() = object : KnowledgeConnection { override val current = MutableStateFlow<KnowledgeRepository?>(this@StoredMeeting) }

    override suspend fun list(kind: KnowledgeKind, tag: String?) = KnowledgeList()
    override suspend fun search(query: String) = emptyList<KnowledgeHit>()
    override suspend fun detail(kind: KnowledgeKind, id: String): KnowledgeDetail {
        reads++
        return KnowledgeDetail("recordings", id, "~/.persona/data/recordings/$id", meta = buildJsonObject {
            put("id", ID); put("name", ID); put("title", "Weekly sync"); put("one_line", "Release dates and who tests the widgets")
            put("started_at", "2026-11-10T14:00:00+08:00"); put("ended_at", "2026-11-10T14:32:05+08:00"); put("duration", 1925)
            put("language", "en-US"); put("device", "phone"); put("complete", true); put("kind", "meeting"); put("minutes_state", state)
        }, summary = "", segments = Segments, minutes = minutes, paused = paused)
    }
    override suspend fun update(kind: KnowledgeKind, id: String, update: KnowledgeUpdate) { writes += "update" }
    override suspend fun makeMinutes(id: String) { writes += "minutes" }
    override suspend fun addMinutesTodos(id: String, version: Int, actions: List<String>) { writes += "todos" }
    override suspend fun handOff(id: String, request: MeetingHandoffRequest): MeetingHandoff { writes += "handoff"; error("not here") }

    companion object {
        const val ID = "20261110-140000-phone"
        val Segments = listOf(
            KnowledgeSegment(1.0, 4.0, "Morning, everyone. Let's start with the release."),
            KnowledgeSegment(95.5, 99.0, "The widgets are ready, so Monday works for the release."),
            KnowledgeSegment(140.0, 144.0, "Then we ship the widgets on Monday."),
            KnowledgeSegment(1210.0, 1214.0, "Sam, the release notes by Friday?"),
        )
        val Minutes = RecordingMinutes(version = 3, revision = 1, title = "Weekly sync",
            start = "2026-11-10T14:00:00+08:00", end = "2026-11-10T14:32:00+08:00", where = "Room 3", attendees = listOf("Alex", "Sam", "Kim"),
            points = listOf(MinutesItem("The widgets are ready; Monday works for the release.", 95.5, "Sam")),
            decisions = listOf(MinutesItem("Ship the widgets on Monday.", 140.0)),
            actions = listOf(
                MinutesAction("a1", "Alex", "Test the watch face on the small watch", "this week", "2026-11-13", 612.0, "todo-1"),
                MinutesAction("a2", "Sam", "Write the release notes", "Friday", "2026-11-14", 1210.0),
                MinutesAction("a3", "Kim", "Book the room for the review", at = 1500.0),
            ),
            markdown = "# Weekly sync\n\n## Key points\n- The widgets are ready; Monday works for the release.")
    }
}

/** One Agent that can take the meeting, so Hand to Agent is offered wherever the page allows it. */
private class ModuleOffSessions : SessionsRepository {
    override val machines = MutableStateFlow(listOf(Machine("mac", name = "Work Mac", online = true)))
    override val sessions = MutableStateFlow(listOf(
        Session(machineId = "mac", sessionId = "site", title = "Website redesign", status = "idle", updatedAt = "2026-11-19T11:00:00Z")))
    override suspend fun refresh() = Unit
}

private object ModuleOffNavigator : Navigator {
    override fun navigate(key: NavKey) = Unit
    override fun back() = Unit
    override fun selectTab(tab: AppTab) = Unit
}

private object ModuleOffSender : MainConversationSender, MessageSender {
    override val outbox = MutableStateFlow(emptyList<OutboxMessage>())
    override suspend fun sendToMain(text: String, clientMessageId: String, draft: DraftSnapshot?) = Unit
    override suspend fun send(session: SessionKey, text: String, clientMessageId: String, clearDraft: String?) = Unit
}

private object ModuleOffConversations : AssistantConversations {
    override val assistant = MutableStateFlow(AssistantSnapshot())
    override val pinned = MutableStateFlow(emptySet<SessionKey>())
    override suspend fun refresh() = Unit
    override suspend fun startConversation(): SessionKey = error("not here")
}
