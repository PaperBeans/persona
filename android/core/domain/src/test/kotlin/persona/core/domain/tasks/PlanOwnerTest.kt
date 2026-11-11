package persona.core.domain.tasks

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.serialization.json.JsonObject
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Test
import persona.core.contracts.FeedItem
import persona.core.contracts.FeedResponse
import persona.core.contracts.ServerEvent
import persona.core.contracts.TaskPlan
import persona.core.contracts.Todo
import persona.core.contracts.api.AssistantManagerChoice
import persona.core.contracts.api.ConnectionState
import persona.core.contracts.api.HubConnection
import persona.core.contracts.api.RequestManager
import persona.core.contracts.api.TasksApi

/**
 * A plan's Start and Dismiss go to the assistant of the plan's conversation (docs/assistant/managers.md "Clients"): the
 * routes name no conversation, so without it the hub takes them to the default, whichever assistant's the plan is.
 */
class PlanOwnerTest {
    @After fun reset() { AssistantManagerChoice.reset(); AssistantManagerChoice.forgetItems() }

    private class Api : TasksApi {
        val named = mutableListOf<Pair<String, String?>>()
        override suspend fun todos() = emptyList<Todo>()
        override suspend fun feed(conversation: String, since: String?) = FeedResponse(emptyList())
        override suspend fun startPlan(id: String, remember: Boolean): TaskPlan {
            named += "start" to currentCoroutineContext()[RequestManager]?.id
            return TaskPlan(id, state = "started")
        }
        override suspend fun dismissPlan(id: String): TaskPlan {
            named += "dismiss" to currentCoroutineContext()[RequestManager]?.id
            return TaskPlan(id, state = "dismissed")
        }
        override suspend fun retryTask(todoId: String): Todo = error("unused")
        override suspend fun removeTask(todoId: String) = Unit
        override suspend fun route(conversation: String, todoId: String, text: String): FeedItem = error("unused")
        override suspend fun today(conversation: String): FeedItem = error("unused")
        override suspend fun mainConversation(): String? = null
        override suspend fun inboxReply(itemId: String, text: String, card: String?) = Unit
    }

    private class Connection : HubConnection {
        override val state = MutableStateFlow(ConnectionState.WaitingForNetwork)
        override val events = MutableSharedFlow<ServerEvent>()
        override fun reconnect() = Unit
        override fun send(message: JsonObject): Boolean = true
    }

    @Test fun aPlanGoesToItsConversationsAssistantWhicheverIsCurrent() {
        val api = Api()
        val store = TasksStore(api, Connection(), CoroutineScope(Dispatchers.Unconfined))
        AssistantManagerChoice.choose("hub", null, "assistant")
        AssistantManagerChoice.noteItem(listOf("assistant", "conversations", "work-main"), "m-work")
        AssistantManagerChoice.noteItem(listOf("assistant", "conversations", "main"), "assistant")
        // The default current: Work's plan still names Work.
        store.startPlan(TaskPlan("p1", "work-main"), remember = false, onError = { throw it })
        // Work current: the default's plan names none, so it goes to the default.
        AssistantManagerChoice.choose("hub", "m-work", "assistant")
        store.dismissPlan(TaskPlan("p2", "main"), onError = { throw it })
        // A conversation no page listed and no row names: as before, nothing named.
        store.dismissPlan(TaskPlan("p3", "elsewhere"), onError = { throw it })
        assertEquals(listOf("start" to "m-work", "dismiss" to null, "dismiss" to null), api.named)
    }

    @Test fun aConversationNoListRegisteredNamesItsOwnSessionsManager() {
        // After a restart the registry is empty: the session row's manager is what names the plan's assistant.
        val api = Api()
        val store = TasksStore(api, Connection(), CoroutineScope(Dispatchers.Unconfined))
        AssistantManagerChoice.choose("hub", null, "assistant")
        store.startPlan(TaskPlan("p1", "work-main"), remember = false, onError = { throw it }, manager = "m-work")
        store.dismissPlan(TaskPlan("p2", "work-main"), onError = { throw it }, manager = "m-work")
        // Its own row wins over a stale registry entry; none known: the registry, then nothing.
        AssistantManagerChoice.noteItem(listOf("assistant", "conversations", "x"), "m-old")
        store.dismissPlan(TaskPlan("p3", "x"), onError = { throw it }, manager = "m-work")
        store.dismissPlan(TaskPlan("p4", "x"), onError = { throw it })
        assertEquals(listOf("start" to "m-work", "dismiss" to "m-work", "dismiss" to "m-work", "dismiss" to "m-old"), api.named)
    }
}
