package persona.android.di

import persona.core.contracts.api.AgentCreationService

/** Keep the model pill's service identity stable until its route or underlying service changes. */
internal fun <K : Any> cachedAgentCreation(
    currentKey: () -> K?,
    create: (K) -> AgentCreationService,
): () -> AgentCreationService? {
    val lock = Any()
    var previous: K? = null
    var service: AgentCreationService? = null
    return {
        synchronized(lock) {
            val key = currentKey()
            if (key != previous) {
                service = key?.let(create)
                previous = key
            }
            service
        }
    }
}
