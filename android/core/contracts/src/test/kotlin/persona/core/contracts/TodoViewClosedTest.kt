package persona.core.contracts

import kotlinx.serialization.json.Json
import org.junit.Assert.assertEquals
import org.junit.Test
import persona.core.contracts.generated.MachineID
import persona.core.contracts.generated.TodoCountsSections
import persona.core.contracts.generated.TodoLineKey
import persona.core.contracts.generated.TodoListSection
import persona.core.contracts.generated.TodoPhase
import persona.core.contracts.generated.TodoReason
import persona.core.contracts.generated.TodoRunRef
import persona.core.contracts.generated.TodoSummary

/**
 * The to-do view's phase, section, reason and a row's line key are closed (typed core plan P4, #15397): a newer hub's value
 * decodes to Unknown with the raw string, never to one this build knows, and writes back as it came; the counts'
 * sections are one field each, read by section; a run's machine and session are ID types.
 */
class TodoViewClosedTest {
    private val codec = Json { encodeDefaults = true; explicitNulls = false }

    @Test fun aNewerHubsValuesDecodeAsUnknown() {
        val json = """{"phase":"a_newer_phase","section":"a_newer_section","reason":"a_newer_reason","line":{"key":"a_newer_line"},"rank":0}"""
        val view = codec.decodeFromString(TodoSummary.serializer(), json)
        assertEquals(TodoPhase.Unknown("a_newer_phase"), view.phase)
        assertEquals(TodoListSection.Unknown("a_newer_section"), view.section)
        assertEquals(TodoReason.Unknown("a_newer_reason"), view.reason)
        assertEquals(TodoLineKey.Unknown("a_newer_line"), TodoLineKey.of(view.line?.key ?: ""))
        assertEquals(codec.parseToJsonElement(json), codec.encodeToJsonElement(TodoSummary.serializer(), view))
        assertEquals(TodoListSection.JustFinished, TodoListSection.of("just_finished"))
    }

    @Test fun theSectionsAreReadBySection() {
        val sections = TodoCountsSections(todo = 2)
        assertEquals(2, sections[TodoListSection.Todo])
        assertEquals(0, sections[TodoListSection.Unknown("a_newer_section")])
        assertEquals(TodoListSection.ALL.size, TodoListSection.ALL.map { sections[it] }.size)
    }

    @Test fun aRunNamesItsMachineAndSessionByID() {
        val run = codec.decodeFromString(TodoRunRef.serializer(), """{"machine_id":"m","session_id":"s"}""")
        assertEquals(MachineID("m"), run.machineId)
        assertEquals("s", run.sessionId.raw)
    }
}
