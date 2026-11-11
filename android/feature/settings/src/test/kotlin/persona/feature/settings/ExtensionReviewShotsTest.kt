package persona.feature.settings

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.hasAnyDescendant
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onFirst
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.JsonCodec
import persona.core.contracts.api.ExtensionFileLines
import persona.core.contracts.api.ExtensionUiResult
import persona.core.contracts.api.ExtensionView
import persona.core.contracts.api.ExtensionViewAction
import persona.core.designsystem.*
import java.io.File

/**
 * UI version 5's code review (#3136) drawn from the component showcase page code-review-v5 on the JVM, every style light and dark,
 * and what pressing it sends: a line picked and widened to a range sent as a draft, hidden lines asked for as lines, a
 * file marked viewed. Written to `build/extension-review-shots/` (or `-Dpersona.shots=<dir>`) to look at.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "zh-w400dp-h4000dp-xhdpi")
class ExtensionReviewShotsTest {
    @get:Rule val rule = createComposeRule()

    private val view = JsonCodec.decodeFromString(ExtensionView.serializer(), File("../../../schema/extension/examples/showcase/code-review-v5.json").readText())
    private val sent = mutableListOf<Pair<String, Map<String, String>>>()
    private val review = CodeReviewState(null) { action, values, done ->
        sent += action.id to values
        done(if (action.id == "context") ExtensionUiResult(lines = ExtensionFileLines(values.getValue("path"), "new", 1, (1..90).map { "\t// line $it" }, 90)) else ExtensionUiResult())
    }
    private val handle = ExtensionViewHandle({ _, _ -> }, { _, _ -> }, {}, {}, act = { a, v -> sent += a.id to v }, review = review)

    private fun out(): File = File(System.getProperty("persona.shots") ?: "build/extension-review-shots").also { it.mkdirs() }

    private fun capture(name: String) {
        rule.waitForIdle()
        repeat(3) { Thread.sleep(150); rule.mainClock.advanceTimeBy(100); rule.waitForIdle() }
        val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
        File(out(), "$name.png").outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
    }

    private fun show(style: AppStyle = AppStyle.Glass, dark: Boolean = false): (AppStyle, Boolean) -> Unit {
        var s by mutableStateOf(style)
        var d by mutableStateOf(dark)
        rule.setContent {
            PersonaTheme(darkTheme = d, reduceMotion = true, style = s) {
                val state = ExtensionViewState(view, values = ExtensionViewText.fields(view))
                key(s, d) { Box(Modifier.testTag("shot").width(400.dp).background(PersonaTheme.palette.background)) {
                    LazyColumn(Modifier.fillMaxWidth(), contentPadding = PaddingValues(Tokens.Space.l)) { extensionViewItems("r", state, handle) }
                } }
            }
        }
        return { a, b -> s = a; d = b }
    }

    @Test fun drawsTheReviewPage() {
        val set = show()
        for (s in AppStyle.entries) for (d in listOf(false, true)) { set(s, d); capture("review-${s.name.lowercase()}-${if (d) "dark" else "light"}") }
    }

    @Test fun picksARangeAndShowsHiddenLines() {
        show()
        rule.onAllNodesWithText("全部").onFirst().performClick()
        rule.waitForIdle()
        assertEquals("context" to mapOf("path" to "src/upload/client.go", "side" to "new", "from" to "1", "to" to "19"), sent.last())
        rule.onAllNodesWithText("// line 19", substring = true).onFirst().assertExists()
        rule.onNodeWithText("var resp *http.Response", substring = true).performClick()
        rule.onAllNodesWithText("})", substring = true).onFirst().performClick()
        rule.waitForIdle()
        capture("review-picked")
        rule.onNodeWithText("评论第 24–28 行", substring = true).assertExists()
    }

    @Test fun anOpenReplyBoxStaysWithItsThread() {
        // drawn by place, as the review's threads are: a thread added above must not get the box opened below it (#4179)
        var threads by mutableStateOf(listOf("t1"))
        rule.setContent {
            PersonaTheme(darkTheme = false, reduceMotion = true) {
                Column { threads.forEach { id -> Box(Modifier.testTag("thread-$id")) { ReplyBox(ExtensionViewAction("reply"), mapOf("thread" to id), ExtensionViewState(), handle) } } }
            }
        }
        rule.onNodeWithText("回复…").performClick()
        rule.waitForIdle()
        rule.onNode(hasTestTag("thread-t1") and hasAnyDescendant(hasText("取消"))).assertExists()
        threads = listOf("t0", "t1")
        rule.waitForIdle()
        rule.onNode(hasTestTag("thread-t0") and hasAnyDescendant(hasText("取消"))).assertDoesNotExist()
    }

    @Test fun marksAFileViewed() {
        show()
        assertTrue(review.viewed("src/upload/retry.go", view))
        review.setViewed("src/upload/client.go", view, true)
        assertEquals("mark_viewed" to mapOf("path" to "src/upload/client.go", "viewed" to "true"), sent.last())
        assertTrue(review.viewed("src/upload/client.go", view))
    }
}
