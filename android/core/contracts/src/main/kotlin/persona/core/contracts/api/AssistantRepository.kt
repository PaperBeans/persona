package persona.core.contracts.api

import persona.core.contracts.Session
import persona.core.contracts.SessionKey
import kotlinx.coroutines.flow.StateFlow

data class AssistantSnapshot(
    val mainSession: SessionKey? = null, val conversations: List<Session> = emptyList(),
    /** Whose conversations these are (docs/assistant/managers.md step 4): the manager the page showed when read; null: the default. */
    val manager: String? = null,
    /** Stable owner identity, including when this manager becomes or ceases to be the default. */
    val managerId: String = manager ?: ManagerRoster.ASSISTANT_MANAGER,
    /**
     * The leader conversations (`assistant.leaders`, docs/assistant/unified-entry.md "Leader conversations"), in the hub's
     * order: the drawer's Directions group. Empty when there are none or from an older hub.
     */
    val leaders: List<String> = emptyList(),
    /** Each direction's name by leader conversation id (`GET /api/v1/assistant/leaders`), shown instead of its title. */
    val directionNames: Map<String, String> = emptyMap(),
)

interface AssistantRepository {
    val assistant: StateFlow<AssistantSnapshot>
    suspend fun refresh()
}
