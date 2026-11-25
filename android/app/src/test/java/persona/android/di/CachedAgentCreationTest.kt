package persona.android.di

import okhttp3.OkHttpClient
import org.junit.Assert.*
import org.junit.Test
import persona.core.hub.HttpAgentCreationService

class CachedAgentCreationTest {
    @Test fun reusesTheServiceUntilTheRouteChanges() {
        var route: String? = "https://primary.example.com"
        val client = OkHttpClient()
        val provider = cachedAgentCreation({ route }) { endpoint ->
            HttpAgentCreationService("hub/generation", endpoint, "test-token", client)
        }

        val first = requireNotNull(provider())
        assertSame(first, provider())
        route = "https://secondary.example.com"
        val second = requireNotNull(provider())
        assertNotSame(first, second)
        assertSame(second, provider())
        route = null
        assertNull(provider())
        route = "https://secondary.example.com"
        assertNotSame(second, requireNotNull(provider()))
    }
}
