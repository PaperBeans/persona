package persona.core.data

import kotlinx.serialization.json.Json
import org.junit.Assert.assertEquals
import org.junit.Test

/** The pairing link's proxied origins are kept with the credentials (#2622); one saved before them reads as fully pinned. */
class HubCredentialsProxiedTest {
    private val json = Json { ignoreUnknownKeys = true; encodeDefaults = true }

    @Test fun aPairingSavedBeforeProxiedOriginsStaysPinned() {
        val old = """{"hubAddress":"https://hub.example.com","hubId":"example-hub","fp":"${"A".repeat(43)}",""" +
            """"candidateAddresses":["https://hub.example.com:19443"],"deviceToken":"token","deviceId":"device"}"""
        assertEquals(emptyList<String>(), json.decodeFromString(HubCredentials.serializer(), old).systemOrigins)
    }

    @Test fun proxiedOriginsSurviveSaving() {
        val saved = HubCredentials("https://hub.example.com", "example-hub", "A".repeat(43), listOf("https://hub.example.com:19443"),
            "token", "device", systemOrigins = listOf("hub.example.com:443"))
        assertEquals(saved, json.decodeFromString(HubCredentials.serializer(), json.encodeToString(HubCredentials.serializer(), saved)))
    }
}
