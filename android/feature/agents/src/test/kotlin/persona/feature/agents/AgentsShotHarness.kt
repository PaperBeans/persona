package persona.feature.agents

import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.emptyPreferences
import androidx.navigation3.runtime.NavKey
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.serialization.json.JsonObject
import persona.core.contracts.*
import persona.core.contracts.api.*
import persona.core.navigation.AppTab
import persona.core.navigation.Navigator

/**
 * A view model over fixed [sessions] and [machines], with some of them [pins]ned or [mutes]d (session ids as `machine/id`),
 * for the screenshots that draw the whole Agents screen: no hub, no network, preferences in memory.
 */
internal fun agentsModel(
    sessions: List<Session>, machines: List<Machine>, pins: List<String> = emptyList(), mutes: List<String> = emptyList(),
): AgentsViewModel = AgentsViewModel(object : SessionsRepository {
    override val sessions = MutableStateFlow(sessions)
    override val machines = MutableStateFlow(machines)
    override suspend fun refresh() = Unit
}, object : InteractionCenter {
    override val pending = MutableStateFlow<List<Interaction>>(emptyList())
    override suspend fun refresh() = Unit
    override suspend fun resolve(id: String, resolution: Resolution) = Unit
}, object : HubConnection {
    override val state = MutableStateFlow(ConnectionState.Online)
    override val events = MutableSharedFlow<ServerEvent>()
    override fun reconnect() = Unit
    override fun send(message: JsonObject) = true
}, object : MessageSender {
    override val outbox = MutableStateFlow<List<OutboxMessage>>(emptyList())
    override suspend fun send(session: SessionKey, text: String, clientMessageId: String, clearDraft: String?) = Unit
}, AgentsPreferences(object : DataStore<Preferences> {
    override val data = MutableStateFlow<Preferences>(emptyPreferences())
    override suspend fun updateData(transform: suspend (Preferences) -> Preferences) = transform(data.value).also { data.value = it }
}, object : HubPreferences {
    override val preferences = MutableStateFlow(HubPreferencesSnapshot(pins = HubPreferenceSet(sessions = pins), mutes = HubPreferenceSet(sessions = mutes)))
    override suspend fun refresh() = Unit
    override suspend fun togglePin(path: String, space: Boolean) = Unit
    override suspend fun toggleMute(path: String, space: Boolean) = Unit
    override suspend fun markSeen(session: SessionKey, version: String) = Unit
}))

/** A navigator that goes nowhere: the screens under test open nothing. */
internal object NoNavigation : Navigator {
    override fun navigate(key: NavKey) = Unit
    override fun back() = Unit
    override fun selectTab(tab: AppTab) = Unit
}
