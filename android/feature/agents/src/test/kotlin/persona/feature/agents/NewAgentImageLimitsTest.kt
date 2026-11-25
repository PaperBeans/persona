package persona.feature.agents

import android.content.Context
import androidx.lifecycle.viewModelScope
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.ImageLimits
import persona.core.contracts.ImageLimitsTable
import persona.core.contracts.Machine
import persona.core.contracts.Session
import persona.core.contracts.SessionKey
import persona.core.contracts.api.AgentCreationService
import persona.core.contracts.api.AgentDirectories
import persona.core.contracts.api.AgentLauncher
import persona.core.contracts.api.AgentStartRequest
import persona.core.contracts.api.AgentStartResult
import persona.core.contracts.api.ChatComposerService
import persona.core.contracts.api.ComposerDelivery
import persona.core.contracts.api.MessageSender
import persona.core.contracts.api.ModelPurposeDetail
import persona.core.contracts.api.OutboxMessage
import persona.feature.chat.ComposerAttachment
import persona.feature.chat.PhotoCandidate
import persona.feature.chat.R as ChatR
import java.io.File

/**
 * Verifies #13043 in `:feature:agents`: New Agent uses the hub's image limits (including per-agent overrides),
 * spills overflow photos past count or body budget into file attachments with localized notices, and formats
 * `agents_attach_photo_limit` across en/zh/ja.
 */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
@OptIn(kotlinx.coroutines.ExperimentalCoroutinesApi::class)
class NewAgentImageLimitsTest {
    private val context = ApplicationProvider.getApplicationContext<Context>()
    private val machines = MutableStateFlow(listOf(Machine("first", "First", online = true)))

    private fun photo(id: String, size: Long = 1024L) =
        ComposerAttachment(id, "$id.jpg", "content://example/$id", image = true, size = size)

    private fun file(id: String) =
        ComposerAttachment(id, "$id.txt", "content://example/$id", image = false, size = 128L)

    private fun newModel(
        limitsTable: ImageLimitsTable = ImageLimitsTable.Standard,
        discarded: MutableList<String> = mutableListOf(),
    ): NewAgentViewModel {
        val composer = object : ChatComposerService {
            override val deliveries = MutableStateFlow(emptyMap<String, ComposerDelivery>())
            override suspend fun upload(session: SessionKey, name: String, file: File): String = "/up/$name"
            override suspend fun interrupt(session: SessionKey) = Unit
            override suspend fun retryRefused(clientMessageId: String, clearDraft: Boolean) = error("unused")
        }
        val uploads = NewAgentUploads(
            service = { composer },
            upload = { service, session, attachment, onStaged ->
                onStaged(attachment.size ?: 1024L)
                service.upload(session, attachment.name, File(attachment.name))
            },
            photosOnly = { "Please look at the photos" },
            filesOnly = { "Please look at the attached files" },
            errorText = { _, e -> e.message.orEmpty() },
            discard = { discarded += it.id },
            imageLimits = { _, agent -> limitsTable.forAgent(agent) },
        )
        val service = object : AgentCreationService {
            override val scope = "hub/pairing"
            override suspend fun launchers(machineId: String) = listOf(AgentLauncher("claude"))
            override suspend fun directories(machineId: String, path: String) = AgentDirectories("/code")
            override suspend fun start(request: AgentStartRequest, operationId: String): AgentStartResult =
                AgentStartResult.Created(Session(machineId = request.machineId, sessionId = "s1"))
            override suspend fun operation(operationId: String): AgentStartResult = error("unused")
            override suspend fun newAgentModel(): ModelPurposeDetail? = null
        }
        val store = object : AgentCreationStore {
            override suspend fun lastMachine(scope: String) = "first"
            override suspend fun defaults(scope: String, machine: String) = AgentDefaults()
            override suspend fun saveDefaults(scope: String, machine: String, defaults: AgentDefaults) = Unit
            override suspend fun pending(scope: String): AgentStartOperation? = null
            override suspend fun pending(scope: String, value: AgentStartOperation?) = Unit
        }
        val sender = object : MessageSender {
            override val outbox = MutableStateFlow(emptyList<OutboxMessage>())
            override suspend fun send(session: SessionKey, text: String, clientMessageId: String, clearDraft: String?) = Unit
        }
        return NewAgentViewModel(service, store, machines, sender, uploads = uploads)
    }

    @Test fun newAgentAllowsTwentyPhotosAndSpillsOverflowToFilesWithNotice() = runTest {
        Dispatchers.setMain(StandardTestDispatcher(testScheduler))
        val model = newModel()
        val notices = mutableListOf<AgentText>()
        val collecting = launch { model.notices.collect { notices += it } }
        try {
            advanceUntilIdle()
            assertEquals(20, model.photoRoom())
            assertEquals(30, model.photoPickerRoom())
            val refused = model.attach(List(22) { photo("p$it") })
            advanceUntilIdle()
            assertEquals(0, refused)
            assertEquals(20, model.state.value.attachments.count { it.image })
            assertEquals(2, model.state.value.attachments.count { !it.media })
            assertEquals(listOf(AgentText(ChatR.string.chat_composer_photos_as_files_other, 20, 2)), notices)
        } finally {
            collecting.cancel()
            model.viewModelScope.cancel()
            Dispatchers.resetMain()
        }
    }

    @Test fun newAgentHonorsPerAgentOverrideAndBodyBudgetSpillAndRefusalWhenFilesFull() = runTest {
        Dispatchers.setMain(StandardTestDispatcher(testScheduler))
        val table = ImageLimitsTable(
            perMessage = 10,
            agents = mapOf(
                "claude" to ImageLimits(
                    requestBytes = ImageLimits.BODY_ROOM + 400L,
                    imageBytes = 3_750_000L,
                    maxSide = 1600,
                    perMessage = 3,
                ),
            ),
        )
        val discarded = mutableListOf<String>()
        val model = newModel(table, discarded)
        val notices = mutableListOf<AgentText>()
        val collecting = launch { model.notices.collect { notices += it } }
        try {
            advanceUntilIdle()
            assertEquals(3, model.photoRoom())
            // Two 150-byte photos = 400 base64 bytes -> fit; third 150-byte photo hits body budget -> spills to file!
            assertEquals(0, model.attach(List(3) { photo("b$it", size = 150L) }))
            advanceUntilIdle()
            assertEquals(2, model.state.value.attachments.count { it.image })
            assertEquals(1, model.state.value.attachments.count { !it.media })
            assertEquals(AgentText(ChatR.string.chat_composer_photos_bytes_as_files_one), notices.removeAt(0))

            // Fill the remaining 9 file slots; the next oversized photo is refused with the body-size notice.
            assertEquals(0, model.attach(List(9) { file("f$it") }))
            advanceUntilIdle()
            assertEquals(1, model.attach(listOf(photo("too-big", size = 150L))))
            advanceUntilIdle()
            assertEquals(listOf("too-big"), discarded)
            assertEquals(AgentText(ChatR.string.chat_composer_photos_too_large), notices.last())
        } finally {
            collecting.cancel()
            model.viewModelScope.cancel()
            Dispatchers.resetMain()
        }
    }

    @Test fun newAgentMultiPickAdmissionIsOrderIndependentWithLimitedFileSlots() = runTest {
        Dispatchers.setMain(StandardTestDispatcher(testScheduler))
        val limits = ImageLimits(
            requestBytes = ImageLimits.BODY_ROOM + 400L,
            imageBytes = 3_750_000L,
            maxSide = 1600,
            perMessage = 20,
        )
        val table = ImageLimitsTable(
            requestBytes = limits.requestBytes,
            imageBytes = limits.imageBytes,
            maxSide = limits.maxSide,
            perMessage = limits.perMessage,
        )
        val largeFirst = newModel(table)
        val smallFirst = newModel(table)
        try {
            advanceUntilIdle()
            // Leave only 1 file slot on both models.
            assertEquals(0, largeFirst.attach(List(9) { file("f$it") }))
            assertEquals(0, smallFirst.attach(List(9) { file("f$it") }))
            advanceUntilIdle()

            val large = PhotoCandidate(photo("large", size = 5000L), scaledBytes = 300L)
            val smallA = PhotoCandidate(photo("smallA", size = 2000L), scaledBytes = 100L)
            val smallB = PhotoCandidate(photo("smallB", size = 2000L), scaledBytes = 150L)

            assertEquals(0, largeFirst.attachPhotos(listOf(large, smallA, smallB)))
            assertEquals(0, smallFirst.attachPhotos(listOf(smallB, smallA, large)))
            advanceUntilIdle()

            assertEquals(setOf("smallA", "smallB"), largeFirst.state.value.attachments.filter { it.image }.map { it.id }.toSet())
            assertEquals(setOf("smallA", "smallB"), smallFirst.state.value.attachments.filter { it.image }.map { it.id }.toSet())
            assertEquals(listOf("smallA", "smallB"), largeFirst.state.value.attachments.filter { it.image }.map { it.id })
            assertEquals(listOf("smallB", "smallA"), smallFirst.state.value.attachments.filter { it.image }.map { it.id })
            assertTrue(largeFirst.state.value.attachments.any { it.id == "large" && !it.image })
            assertTrue(smallFirst.state.value.attachments.any { it.id == "large" && !it.image })
        } finally {
            largeFirst.viewModelScope.cancel()
            smallFirst.viewModelScope.cancel()
            Dispatchers.resetMain()
        }
    }

    @Config(qualifiers = "en")
    @Test fun englishAgentsPhotoLimitFormatsDynamicCount() {
        assertEquals("Up to 20 at a time", context.getString(R.string.agents_attach_photo_limit, 20))
    }

    @Config(qualifiers = "zh")
    @Test fun chineseAgentsPhotoLimitFormatsDynamicCount() {
        assertEquals("一次最多 20 张", context.getString(R.string.agents_attach_photo_limit, 20))
    }

    @Config(qualifiers = "ja")
    @Test fun japaneseAgentsPhotoLimitFormatsDynamicCount() {
        assertEquals("一度に20枚まで", context.getString(R.string.agents_attach_photo_limit, 20))
    }
}
