package persona.core.contracts

import java.io.File
import kotlinx.serialization.json.*
import org.junit.Assert.*
import org.junit.Test
import persona.core.contracts.generated.CountsHeldReason
import persona.core.contracts.generated.CountsHeldReasonSerializer
import persona.core.contracts.generated.CountsHeldRow
import persona.core.contracts.generated.CountsView

/**
 * The counts views of spec/fixtures/counts/view_samples.json (schema/wire/counts.schema.json, #15512) read into the
 * generated CountsView (generated/CountsWire.kt) and write back as the same JSON. The codec writes required fields even
 * at their defaults and leaves absent optional ones out, as the manager does. A reason this app does not know is kept
 * as it came, and a count a manager did not send stays absent rather than 0.
 */
class CountsWireTest {
    private val codec = Json { encodeDefaults = true; explicitNulls = false }

    @Test fun writesEverySampleBack() {
        val root = File(System.getProperty("persona.repo.root"))
        val cases = Json.parseToJsonElement(File(root, "spec/fixtures/counts/view_samples.json").readText())
            .jsonObject.getValue("cases").jsonArray
        assertTrue(cases.isNotEmpty())
        for (case in cases) {
            val view = case.jsonObject.getValue("expected").jsonObject.getValue("view")
            val written = codec.encodeToJsonElement(codec.decodeFromJsonElement<CountsView>(view))
            assertEquals(case.jsonObject.getValue("name").toString(), view, written)
        }
    }

    @Test fun keepsAReasonItDoesNotKnow() {
        val row = codec.decodeFromString<CountsHeldRow>("""{"id":"agent:studio/q","why":"quiet"}""")
        assertEquals(CountsHeldReason.Unknown("quiet"), row.why)
        assertEquals(""""quiet"""", codec.encodeToString(CountsHeldReasonSerializer, row.why))
        assertEquals(CountsHeldReason.Muted, CountsHeldReason.of("muted"))
    }

    @Test fun aCountNotSentIsAbsent() {
        val view = codec.decodeFromString<CountsView>(
            """{"epoch":"e1","rev":1,"as_of":1795593600000,"build":"1.0.0","counts":{}}""")
        assertNull(view.counts.agentsNeedsYou)
        assertNull(view.totals)
        assertEquals(1_795_593_600_000L, view.asOf)
    }
}
