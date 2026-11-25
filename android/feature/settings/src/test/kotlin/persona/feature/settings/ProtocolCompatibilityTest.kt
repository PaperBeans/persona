package persona.feature.settings

import org.junit.Assert.*
import org.junit.Test

class ProtocolCompatibilityTest {
    @Test fun baselineExtensionSourceLinksUseTheSameInAppTargetAsCurrentLinks() {
        for (scheme in listOf("jarvis", "persona")) {
            assertEquals("test:item", ExtensionViewText.inboxSource("$scheme://inbox/test%3Aitem/source"))
            assertNull(ExtensionViewText.inboxSource("$scheme://user@example.com/inbox/test/source"))
            assertNull(ExtensionViewText.inboxSource("$scheme://inbox/test/source?other=1"))
        }
        assertNull(ExtensionViewText.inboxSource("other://inbox/test/source"))
    }

    @Test fun spaceConversationLinkStaysWithinItsExtension() {
        val link = "persona://tracks/computer-example/reading/session-example"
        assertEquals("computer-example" to "session-example", ExtensionViewText.spaceConversation(link))
        assertTrue(ExtensionViewText.opens("space-conversations", link))
        assertFalse(ExtensionViewText.opens("other", link))
        assertNull(ExtensionViewText.spaceConversation("persona://tracks/computer-example/health/session-example"))
        assertNull(ExtensionViewText.spaceConversation("persona://settings/extensions/space-conversations"))
        assertNull(ExtensionViewText.spaceConversation("https://example.com/tracks/computer-example/reading/session-example"))
    }
}
