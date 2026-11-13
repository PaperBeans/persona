package persona.core.hub

import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.jsonObject
import okhttp3.Protocol
import okhttp3.Request
import okhttp3.Response
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Assert.*
import org.junit.Test
import persona.core.contracts.JsonCodec

/** Change answer's request (docs/protocol/protocol.md POST …/reanswer, iOS `APIClient.reanswer`): path, manager and body. */
class ReanswerRequestTest {
    private class Sent(val path: String, val method: String, val body: String)

    private fun reply(path: String, code: Int, body: String) = Response.Builder().request(Request.Builder().url("https://hub.example$path").build())
        .protocol(Protocol.HTTP_1_1).code(code).message("x").body(body.toResponseBody()).build()

    @Test fun theChangedAnswersGoToTheCardsManager() = runTest {
        val sent = mutableListOf<Sent>()
        val transport = HubChatTransport({ path, method, body, _ ->
            sent += Sent(path, method, okio.Buffer().also { body!!.writeTo(it) }.readUtf8())
            reply(path, 200, """{"ok":true}""")
        })
        transport.reanswer("ix_1", mapOf("Which driver?" to "sqlc"), "work")
        transport.reanswer("ix 2", mapOf("Which driver?" to "pgx"), null)
        assertEquals("/api/v1/interactions/ix_1/reanswer?manager=work", sent[0].path)
        assertEquals("POST", sent[0].method)
        assertEquals(mapOf("answers" to mapOf("Which driver?" to "sqlc")),
            JsonCodec.parseToJsonElement(sent[0].body).jsonObject.mapValues { (_, v) -> v.jsonObject.mapValues { it.value.toString().trim('"') } })
        // One manager, or an older hub: nothing named, the default manager answers.
        assertEquals("/api/v1/interactions/ix%202/reanswer", sent[1].path)
    }

    @Test fun aRefusalCarriesTheHubsStatusAndCode() = runTest {
        var attempts = 0
        val transport = HubChatTransport({ path, _, _, _ ->
            attempts++
            reply(path, 503, """{"error":"assistant offline","code":"assistant_offline"}""")
        })
        val failure = runCatching { transport.reanswer("ix_1", mapOf("Which driver?" to "sqlc"), null) }.exceptionOrNull() as HubFailure
        assertEquals(503, failure.status)
        assertEquals("assistant_offline", failure.code)
        assertEquals("not retried: the hub sends the same answer once", 1, attempts)
    }
}
