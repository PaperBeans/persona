package persona.core.contracts

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import persona.core.contracts.api.KnowledgeDetailResponse
import persona.core.contracts.api.KnowledgeList
import persona.core.contracts.api.RecordingPaused

/** The hub's `paused` on a recording's list entry and detail (#13407, #15214) as a closed set with an unknown. */
class RecordingPausedContractTest {
    private fun detail(paused: String?) = JsonCodec.decodeFromString(KnowledgeDetailResponse.serializer(),
        """{"detail":{"kind":"recordings","id":"r1","path":"data/recordings/r1"${paused?.let { ""","paused":$it""" }.orEmpty()}}}""").detail

    private fun entry(paused: String?) = JsonCodec.decodeFromString(KnowledgeList.serializer(),
        """{"items":[{"kind":"recordings","id":"r1","name":"Weekly sync"${paused?.let { ""","paused":$it""" }.orEmpty()}}]}""").items.single()

    @Test fun theHubsModuleOffMarkIsRead() {
        assertEquals(RecordingPaused.ModuleOff, detail("\"module_off\"").paused)
        assertEquals(RecordingPaused.ModuleOff, entry("\"module_off\"").paused)
    }

    @Test fun aRecordingThatIsNotPausedHasNone() {
        // The hub leaves the field out; an explicit null reads the same.
        assertNull(detail(null).paused)
        assertNull(entry(null).paused)
        assertNull(detail("null").paused)
    }

    @Test fun aReasonThisAppDoesNotKnowIsUnknownAndNeverModuleOff() {
        for (value in listOf("\"retention_hold\"", "\"MODULE_OFF\"", "\"\"")) {
            assertEquals(value, RecordingPaused.Unknown, detail(value).paused)
            assertEquals(value, RecordingPaused.Unknown, entry(value).paused)
        }
    }

    @Test fun itGoesBackOnTheWireByItsName() {
        val again = JsonCodec.encodeToString(KnowledgeDetailResponse.serializer(), KnowledgeDetailResponse(detail("\"module_off\"")))
        assertEquals(RecordingPaused.ModuleOff, JsonCodec.decodeFromString(KnowledgeDetailResponse.serializer(), again).detail.paused)
    }
}
