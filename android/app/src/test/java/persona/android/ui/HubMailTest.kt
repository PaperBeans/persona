package persona.android.ui

import org.junit.Assert.assertEquals
import org.junit.Test
import persona.core.designsystem.MailAccess

/** The one place the app turns the hub's mail module flag into what pages may do with mail (#15995). */
class HubMailTest {
    @Test fun mailIsLiveUnlessAHubThatPublishesModuleFlagsLeavesItOut() {
        // No hello yet, demo mode and an older hub without the marker: as it always was.
        assertEquals(MailAccess.Live, mailAccessOf(emptySet()))
        assertEquals(MailAccess.Live, mailAccessOf(setOf("extensions", "inbox_sources")))
        // A flag without the marker changes nothing, and neither does its absence.
        assertEquals(MailAccess.Live, mailAccessOf(setOf("module.mail")))
        assertEquals(MailAccess.Live, mailAccessOf(setOf("module.lark")))
        // A hub that publishes module flags: live with its flag, stored text only without it.
        assertEquals(MailAccess.Live, mailAccessOf(setOf("modules", "module.mail")))
        assertEquals(MailAccess.StoredOnly, mailAccessOf(setOf("modules")))
        assertEquals(MailAccess.StoredOnly, mailAccessOf(setOf("modules", "module.lark", "module.recording", "inbox_sources")))
        // The two states are the whole set.
        assertEquals(listOf(MailAccess.Live, MailAccess.StoredOnly), MailAccess.entries)
    }
}
