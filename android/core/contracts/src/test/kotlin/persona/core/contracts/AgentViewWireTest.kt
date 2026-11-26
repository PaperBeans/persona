package persona.core.contracts

import java.io.File
import kotlinx.serialization.json.*
import org.junit.Assert.*
import org.junit.Test
import persona.core.contracts.generated.AgentPhase
import persona.core.contracts.generated.AgentReason
import persona.core.contracts.generated.AgentView
import persona.core.contracts.generated.ReasonCards

/** The views of spec/fixtures/agent-view (schema/wire/agent-view.schema.json, #15397) read into the generated AgentView and write back as the same JSON. */
class AgentViewWireTest {
    private val codec = Json { explicitNulls = false }

    @Test fun writesEveryViewBack() {
        val root = File(System.getProperty("persona.repo.root"))
        val cases = Json.parseToJsonElement(File(root, "spec/fixtures/agent-view/cases.json").readText())
            .jsonObject.getValue("cases").jsonArray
        var n = 0
        for (case in cases) {
            for (entry in case.jsonObject.getValue("expected").jsonObject.getValue("views").jsonArray) {
                val id = entry.jsonObject.getValue("id").jsonPrimitive.content
                val value = entry.jsonObject.getValue("view")
                val view = codec.decodeFromJsonElement<AgentView>(value)
                assertFalse(id, view.phase is AgentPhase.Unknown)
                assertFalse(id, view.reason is AgentReason.Unknown)
                assertEquals(id, value, codec.encodeToJsonElement(view))
                n++
            }
        }
        assertTrue(n > 0)
    }

    @Test fun keepsWhatANewerHubSends() {
        val view = codec.decodeFromString<AgentView>("""{"phase":"paused","reason":{"kind":"snoozed","x":1},"rank":50}""")
        assertEquals(AgentPhase.Unknown("paused"), view.phase)
        assertEquals("snoozed", view.reason?.kind)
        assertTrue(view.reason is AgentReason.Unknown)
        val cards = codec.decodeFromString<AgentView>("""{"phase":"needs_you","reason":{"kind":"cards","n":2},"counted":true,"rank":0}""")
        assertEquals(ReasonCards(n = 2), cards.reason)
    }
}
