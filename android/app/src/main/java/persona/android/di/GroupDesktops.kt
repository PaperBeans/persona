package persona.android.di

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.flow.update
import persona.core.contracts.HubDesktop
import persona.core.contracts.api.ConnectionState

/**
 * The desktop apps signed in to each hub group (its `desktops` event), and the selected group's list for
 * Settings › Notifications › "Signed-in computers". The event carries no group, so each group's list is kept apart: another hub's Mac must not
 * show up (or be revoked) on the selected hub. A group's list is unknown while it is not connected.
 */
internal class GroupDesktops(scope: CoroutineScope, selected: StateFlow<String?>) {
    private val byGroup = MutableStateFlow<Map<String, List<HubDesktop>>>(emptyMap())

    /** The selected group's desktops; empty when none is selected or it has not sent the list yet. */
    val selected: StateFlow<List<HubDesktop>> = combine(selected, byGroup) { id, lists -> id?.let { lists[it] }.orEmpty() }
        .stateIn(scope, SharingStarted.Eagerly, emptyList())

    fun onDesktops(group: String, desktops: List<HubDesktop>) = byGroup.update { it + (group to desktops) }

    fun online(group: String): Boolean? = byGroup.value[group]?.isNotEmpty()

    fun onState(group: String, state: ConnectionState) {
        // Updating is a connected socket catching up (the hub sent the list on connecting).
        if (state != ConnectionState.Online && state != ConnectionState.Updating) byGroup.update { it - group }
    }

    /** Drops the lists of groups that are gone. */
    fun retain(groups: Set<String>) = byGroup.update { lists -> lists.filterKeys { it in groups } }
}
