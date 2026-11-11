package persona.core.contracts

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/** The refusal fallback on the session and the refusal record on a notice (docs/protocol/protocol.md, #2528). */
class ModelRefusalContractTest {
    @Test fun sessionCarriesTheFallbackAndAPendingRestore() {
        val s = JsonCodec.decodeFromString<Session>(
            """{"machine_id":"m","session_id":"s","turn_error_code":"model_fallback_unavailable",
               "model_fallback":{"subtype":"model_refusal_fallback","original_model":"primary-model","fallback_model":"fallback-model",
                 "category":"synthetic-policy","scope":"session","explanation":"Upstream refused","text":"words"},
               "model_restore":{"state":"next_start","to":"configured-model","at":"2026-11-11T08:00:00Z"}}""")
        assertEquals("model_fallback_unavailable", s.turnErrorCode)
        assertEquals(ModelRefusal("model_refusal_fallback", "primary-model", "fallback-model", "synthetic-policy", "session", "Upstream refused", "words"), s.modelFallback)
        assertEquals("next_start", s.modelRestore?.state)
        assertEquals("configured-model", s.modelRestore?.to)
    }

    @Test fun anOlderHubsRawLineIsNotKept() {
        val it = JsonCodec.decodeFromString<Item>(
            """{"id":"n","kind":"notice","text":"words","model_refusal":{"subtype":"model_refusal_fallback","raw":"{\"secret\":1}","text":"words"}}""")
        assertEquals(ModelRefusal("model_refusal_fallback", text = "words"), it.modelRefusal)
        assertEquals(false, JsonCodec.encodeToString(Item.serializer(), it).contains("secret"))
        assertNull(JsonCodec.decodeFromString<Session>("""{"session_id":"s"}""").modelFallback)
    }
}
