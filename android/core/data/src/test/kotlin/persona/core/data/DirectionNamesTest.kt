package persona.core.data

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonObject
import org.junit.Assert.assertEquals
import org.junit.Test

// The drawer's Directions (docs/assistant/unified-entry.md "Leader conversations"): `assistant.leaders` in the hub's order,
// named by GET /api/v1/assistant/leaders.
class DirectionNamesTest {
    private fun json(text: String): JsonObject = Json.parseToJsonElement(text).jsonObject

    @Test fun leadersKeepTheHubsOrderWithoutTheMainOne() {
        assertEquals(listOf("b", "a"), leadersOf(json("""{"main": "m", "leaders": ["b", "", "m", "a"]}""")))
        assertEquals(emptyList<String>(), leadersOf(json("""{"main": "m"}""")))
    }

    @Test fun namesSkipEndedAndBlankOnes() {
        val names = directionNamesOf(json("""{"leaders": [
            {"id": "a", "name": " Product "},
            {"id": "b", "name": ""},
            {"id": "c", "name": "Old", "ended": true},
            {"id": "", "name": "Nobody"}
        ]}"""))
        assertEquals(mapOf("a" to "Product"), names)
        assertEquals(emptyMap<String, String>(), directionNamesOf(json("{}")))
    }
}
