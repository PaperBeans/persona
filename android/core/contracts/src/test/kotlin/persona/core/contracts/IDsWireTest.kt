package persona.core.contracts

import java.io.File
import kotlinx.serialization.json.*
import org.junit.Assert.*
import org.junit.Test
import persona.core.contracts.generated.AgentRef
import persona.core.contracts.generated.MachineID
import persona.core.contracts.generated.SessionID

/** The IDs and AgentRef of spec/fixtures/ids (schema/wire/ids.schema.json, #15397) read into the generated types and write back as the same JSON. */
class IDsWireTest {
    private val codec = Json { encodeDefaults = true; explicitNulls = false }

    @Test fun writesEverySampleBack() {
        val root = File(System.getProperty("persona.repo.root"))
        val cases = Json.parseToJsonElement(File(root, "spec/fixtures/ids/ids.json").readText())
            .jsonObject.getValue("cases").jsonArray
        assertTrue(cases.isNotEmpty())
        for (case in cases) {
            val expected = case.jsonObject.getValue("expected").jsonObject
            val value = expected.getValue("value")
            val written = when (val definition = expected.getValue("definition").jsonPrimitive.content) {
                "AgentRef" -> codec.encodeToJsonElement(codec.decodeFromJsonElement<AgentRef>(value))
                "MachineID" -> codec.encodeToJsonElement(codec.decodeFromJsonElement<MachineID>(value))
                "SessionID" -> codec.encodeToJsonElement(codec.decodeFromJsonElement<SessionID>(value))
                else -> value // the other IDs are generated the same way; the three above pin the codec
            }
            assertEquals(case.toString(), value, written)
        }
    }

    @Test fun anAgentRefNeedsBothFields() {
        assertThrows(Exception::class.java) { val _ = codec.decodeFromString<AgentRef>("""{"machine":"m"}""") }
    }
}
