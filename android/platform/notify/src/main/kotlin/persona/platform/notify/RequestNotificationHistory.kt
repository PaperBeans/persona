package persona.platform.notify

import android.content.Context

/**
 * Request cards this process or an earlier one actually posted. Dismissing a card does not make the same pending
 * request new again; resolving it or unpairing clears its record. Ids are local notification state, never sent anywhere.
 */
internal class RequestNotificationHistory(context: Context) {
    private val store = context.applicationContext.getSharedPreferences("request-notification-history", Context.MODE_PRIVATE)

    fun ids(): Set<String> = store.all.keys.mapNotNullTo(HashSet()) { it.removePrefix(PREFIX).takeIf { id -> it.startsWith(PREFIX) } }

    fun mark(id: String) { store.edit().putBoolean(PREFIX + id, true).apply() }

    fun clear(id: String) { store.edit().remove(PREFIX + id).apply() }

    fun clear() { store.edit().clear().apply() }

    private companion object { const val PREFIX = "request:" }
}
