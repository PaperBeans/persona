package persona.platform.notify

import org.junit.Assert.assertEquals
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class RequestNotificationHistoryTest {
    @Test fun dismissedRequestsStayDeliveredUntilResolvedOrCleared() {
        val context = RuntimeEnvironment.getApplication()
        val history = RequestNotificationHistory(context)
        history.clear()
        history.mark("one")
        history.mark("two")
        assertEquals(setOf("one", "two"), RequestNotificationHistory(context).ids())
        history.clear("one")
        assertEquals(setOf("two"), RequestNotificationHistory(context).ids())
        history.clear()
        assertEquals(emptySet<String>(), RequestNotificationHistory(context).ids())
    }
}
