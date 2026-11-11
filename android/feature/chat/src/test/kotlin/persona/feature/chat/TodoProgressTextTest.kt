package persona.feature.chat

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config

/** A to-do list's progress line follows the total in the English plural form (#4079). */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en")
class TodoProgressTextTest {
    private val res get() = ApplicationProvider.getApplicationContext<Context>().resources

    @Test fun nounFollowsTheTotal() {
        assertEquals("0/1 to-do done", res.getQuantityString(R.plurals.chat_todos_progress, 1, 0, 1))
        assertEquals("1/1 to-do done", res.getQuantityString(R.plurals.chat_todos_progress, 1, 1, 1))
        assertEquals("1/3 to-dos done", res.getQuantityString(R.plurals.chat_todos_progress, 3, 1, 3))
    }
}
