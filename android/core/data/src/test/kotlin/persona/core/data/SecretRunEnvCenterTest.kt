package persona.core.data

import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import persona.core.contracts.Interaction
import persona.core.contracts.Resolution

/** Only a full approval of the actual pending env card carries secret_run_env. */
class SecretRunEnvCenterTest {
    private val env = Interaction("ix", machineId = "hub", sessionId = "chat", kind = "permission", toolName = "secret_run",
        toolInput = buildJsonObject {
            put("commands", JsonArray(listOf(JsonPrimitive("true"))))
            put("delivery", "env")
        })
    private val stdin = env.copy(id = "stdin", toolInput = buildJsonObject {
        put("commands", JsonArray(listOf(JsonPrimitive("true"))))
        put("delivery", "stdin")
    })

    @Test fun onlyThePendingEnvCardIsMarked() = runTest {
        suspend fun forwarded(card: Interaction?, id: String, resolution: Resolution): Pair<Boolean?, Boolean?> {
            val transport = FakeChatTransport().apply { pending = listOfNotNull(card) }
            val center = HubInteractionCenter(transport, FakeConnection(), backgroundScope)
            center.refresh()
            center.resolveOutcome(id, resolution, handoffCard = true)
            return transport.handoffCard to transport.secretRunEnv
        }
        assertTrue(forwarded(env, env.id, Resolution(allow = true)) == (true to true))
        assertTrue(forwarded(env, env.id, Resolution(allow = false)) == (false to false))
        assertFalse(forwarded(stdin, stdin.id, Resolution(allow = true)) == (true to true))
        assertFalse(forwarded(null, "stale", Resolution(allow = true)) == (true to true))
    }
}
