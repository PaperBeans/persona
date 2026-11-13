package persona.android.di

import android.util.Log
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.withTimeout
import persona.core.hub.OkHttpHubConnection

/**
 * Revokes this device's credential on its hub (#5538, as Apple's HubSignOut.swift #1824): the hub then drops the
 * device's pushes and Live Activities. Best-effort: a hub that cannot be reached is not retried, and the caller's
 * local cleanup still runs.
 */
internal suspend fun revokeCredential(connection: OkHttpHubConnection, deviceId: String) {
    if (deviceId.isBlank()) return
    try {
        withTimeout(10_000L) {
            connection.request("/api/v1/auth/tokens/$deviceId", "DELETE")
        }
    } catch (error: CancellationException) { throw error }
    catch (error: Exception) {
        Log.w("RevokeCredential", "Could not revoke device credential on hub: ${error.message}")
    }
}
