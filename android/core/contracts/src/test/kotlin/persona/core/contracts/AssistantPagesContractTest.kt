package persona.core.contracts

import kotlinx.serialization.json.jsonObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertSame
import org.junit.Test
import persona.core.contracts.api.AssistantConfig
import persona.core.contracts.api.AssistantPagesFeature

/** Dashboards (#15017) on the wire: the hub's `assistant_pages` event and the feature that gates the switch. */
class AssistantPagesContractTest {
    @Test fun theListChangedEventDecodesFromRawJsonWithOrWithoutAPayload() {
        assertSame(AssistantPagesEvent, ServerEvent.decode("""{"type":"assistant_pages"}"""))
        // The hub sends {}; anything a later hub adds changes nothing.
        assertSame(AssistantPagesEvent, ServerEvent.decode("""{"type":"assistant_pages","manager":"m-2","reason":"pinned"}"""))
    }

    @Test fun theEventEncodesAsTheHubSendsIt() {
        val encoded = JsonCodec.parseToJsonElement(JsonCodec.encodeToString(ServerEvent.serializer(), AssistantPagesEvent)).jsonObject
        assertEquals(mapOf("type" to "assistant_pages"), encoded.mapValues { it.value.toString().trim('"') })
    }

    @Test fun theSwitchIsOffWhenTheHubLeavesItOut() {
        assertEquals("assistant_pages", AssistantPagesFeature)
        assertEquals(null, JsonCodec.decodeFromString(AssistantConfig.serializer(), """{"machine_id":"server"}""").dashboards)
        assertEquals(true, JsonCodec.decodeFromString(AssistantConfig.serializer(), """{"dashboards":true}""").dashboards)
        assertEquals(false, JsonCodec.decodeFromString(AssistantConfig.serializer(), """{"dashboards":false}""").dashboards)
    }
}
