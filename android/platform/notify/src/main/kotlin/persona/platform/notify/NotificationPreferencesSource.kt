package persona.platform.notify

import persona.core.contracts.api.DeviceNotificationPreferences
import kotlinx.coroutines.flow.StateFlow

interface NotificationPreferencesSource {
    val preferences: StateFlow<DeviceNotificationPreferences?>
    suspend fun refreshPreferences(): DeviceNotificationPreferences?

    /** One entry per open hub group, keyed by its merged-id namespace (null for the primary group). */
    suspend fun refreshPreferencesForCatchUp(): Map<String?, DeviceNotificationPreferences> =
        refreshPreferences()?.let { mapOf<String?, DeviceNotificationPreferences>(null to it) }.orEmpty()

    /** The namespaced machine id identifies which hub group's desktop presence applies; null is not known yet. */
    fun desktopOnline(machineId: String): Boolean? = null
}
