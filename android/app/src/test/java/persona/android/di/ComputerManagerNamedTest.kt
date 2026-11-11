package persona.android.di

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/** The add names the current assistant only on the hub that assistant is on (its id means nothing to another hub). */
class ComputerManagerNamedTest {
    @Test fun namedOnlyOnTheHubThePickIsOf() {
        assertEquals("m-3fa21c", managerNamedFor("m-3fa21c", "hub-a", "hub-a"))
        assertNull(managerNamedFor("m-3fa21c", "hub-a", "hub-b"))
        assertNull(managerNamedFor(null, "hub-a", "hub-a"))
    }
}
