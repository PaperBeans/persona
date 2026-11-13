package persona.core.data

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.buildJsonArray
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import persona.core.contracts.JsonCodec
import persona.core.contracts.Machine
import persona.core.contracts.ModelPick
import persona.core.contracts.Session
import persona.core.contracts.api.InboxAssistantConnection
import persona.core.contracts.api.InboxAssistantTransport
import persona.core.contracts.api.SessionsRepository

/**
 * Summary rows landing on Persona's conversations (#5055, riba2534's note on #5055): the launcher, the permission mode
 * and the compaction the conversation had stay, its `model_pick` is the summary's (absent: it follows the default again).
 * And a new chat's start carries the model and effort picked on the new-chat page.
 */
@OptIn(kotlinx.coroutines.ExperimentalCoroutinesApi::class)
class AssistantSummaryRowsTest {
    private class Transport : InboxAssistantTransport {
        val created = mutableListOf<ModelPick?>()
        override val scope = "scope"
        override suspend fun assistant() = buildJsonObject {
            put("machine_id", "mac"); put("main", "main")
            put("conversations", buildJsonArray {
                add(JsonCodec.parseToJsonElement("""{"machine_id":"mac","session_id":"c1","assistant":true,"launcher":"team",
                    "permission_mode":"plan","effort":"high","model":"sonnet","compaction":{"state":"done"},
                    "model_pick":{"launcher":"team","model":"sonnet"}}"""))
            })
        }
        override suspend fun createConversation(main: Boolean) = create(null)
        override suspend fun createConversationOf(manager: String?, main: Boolean, pick: ModelPick) = create(pick)
        private fun create(pick: ModelPick?): JsonObject {
            created += pick
            return buildJsonObject { put("session", buildJsonObject { put("machine_id", "mac"); put("session_id", "n${created.size}"); put("assistant", true) }) }
        }
        override suspend fun pins() = buildJsonObject { put("sessions", buildJsonArray { }) }
        override suspend fun inbox() = error("unused")
        override suspend fun setDone(itemId: String, done: Boolean) = error("unused")
        override suspend fun reply(itemId: String?, feedId: String?, text: String, clientId: String) = error("unused")
        override suspend fun messages(feedId: String, around: Long?, before: String?, after: String?) = error("unused")
        override suspend fun mutes() = error("unused")
        override suspend fun media(ref: String) = error("unused")
    }

    private class Sessions : SessionsRepository {
        override val machines: StateFlow<List<Machine>> = MutableStateFlow(emptyList())
        override val sessions = MutableStateFlow<List<Session>>(emptyList())
        override suspend fun refresh() {}
    }

    @Test fun aSummaryRowKeepsTheConversationsDetailsAndBringsItsPick() = runTest {
        val transport = Transport()
        val sessions = Sessions()
        val connection = object : InboxAssistantConnection { override val current = MutableStateFlow<InboxAssistantTransport?>(transport) }
        val repository = RealAssistantRepository(connection, sessions, backgroundScope)
        runCurrent()
        repository.refresh()
        runCurrent()
        fun conversation() = repository.assistant.value.conversations.single { it.sessionId == "c1" }
        assertEquals("team", conversation().launcher)

        // a paged list's summary: the model and the pick it carries, nothing of the chat's details
        sessions.sessions.value = listOf(JsonCodec.decodeFromString("""{"machine_id":"mac","session_id":"c1","assistant":true,"waiting":false,
            "model":"opus","effort":"low","model_pick":{"launcher":"claude","model":"opus"}}"""))
        runCurrent()
        assertEquals("team", conversation().launcher)
        assertEquals("plan", conversation().permissionMode)
        assertEquals("done", conversation().compaction?.state)
        assertEquals("opus" to "low", conversation().model to conversation().effort)
        assertEquals(ModelPick("claude", "opus"), conversation().modelPick)

        // Follow default on another device: the next summary has no pick
        sessions.sessions.value = listOf(JsonCodec.decodeFromString("""{"machine_id":"mac","session_id":"c1","assistant":true,"waiting":false}"""))
        runCurrent()
        assertNull(conversation().modelPick)
        assertEquals("team", conversation().launcher)
    }

    @Test fun aNewChatStartsOnThePickedSetup() = runTest {
        val transport = Transport()
        val connection = object : InboxAssistantConnection { override val current = MutableStateFlow<InboxAssistantTransport?>(transport) }
        val repository = RealAssistantRepository(connection, Sessions(), backgroundScope)
        runCurrent()
        repository.startConversation(ModelPick("team", "sonnet", "low"))
        repository.startConversation()
        assertEquals(listOf(ModelPick("team", "sonnet", "low"), null), transport.created)
        repository.assistant.first { it.conversations.any { c -> c.sessionId == "n1" } }
    }
}
