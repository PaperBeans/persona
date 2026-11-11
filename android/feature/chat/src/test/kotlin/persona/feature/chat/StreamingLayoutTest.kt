package persona.feature.chat

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import coil3.ImageLoader
import kotlinx.coroutines.*
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestCoroutineScheduler
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.chatlist.*
import persona.core.contracts.Item
import persona.core.contracts.SessionKey
import persona.core.designsystem.*
import persona.core.domain.lines.ChatLine
import java.io.File

/** Replay both hosts' real chat rows and reverse list; hold only the parser executor between UI frames. */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h900dp-xhdpi")
class StreamingLayoutTest {
    @get:Rule val rule = createComposeRule()

    @Test fun assistantStreamingLayout() = replay(ChatRenderStyle.Assistant, "assistant")
    @Test fun agentStreamingLayout() = replay(ChatRenderStyle.Agent, "agent")

    @Test fun changingMessageIdentityClearsThePreviousParse() {
        val scheduler = TestCoroutineScheduler()
        val parserScope = CoroutineScope(SupervisorJob() + StandardTestDispatcher(scheduler))
        val cache = ChatMarkdownCache(scope = parserScope)
        var id by mutableStateOf("first")
        var text by mutableStateOf("## Previous message")
        val warm = parserScope.async { cache.parse(id, text) }
        scheduler.runCurrent()
        runBlocking { warm.await() }
        try {
            rule.setContent {
                PersonaTheme(reduceMotion = true) {
                    CompositionLocalProvider(LocalChatMarkdownCache provides cache) { ChatMarkdown(text, id) }
                }
            }
            rule.onNodeWithText("Previous message").assertExists()
            id = "second"; text = "Different message"
            rule.waitForIdle()
            rule.onNodeWithText("Previous message").assertDoesNotExist()
            rule.onNodeWithText("Different message").assertExists()
            scheduler.runCurrent(); rule.waitForIdle()
            rule.onNodeWithText("Different message").assertExists()
        } finally { parserScope.cancel() }
    }

    private fun replay(style: ChatRenderStyle, host: String) {
        val scheduler = TestCoroutineScheduler()
        val parserScope = CoroutineScope(SupervisorJob() + StandardTestDispatcher(scheduler))
        val cache = ChatMarkdownCache(scope = parserScope)
        val initial = "## Streaming report\n\n" + "A long paragraph wraps across several lines without losing any words. ".repeat(5)
        var words by mutableStateOf(initial)
        var streaming by mutableStateOf(true)
        val id = "reply"
        var height = 0
        lateinit var list: ChatListState
        val output = File("build/reports/tests/streaming-layout/$host").also { it.mkdirs() }
        val readings = mutableListOf("step,phase,height,index,offset,endOffset,rowSize")
        val failures = mutableListOf<String>()
        var passed = false
        fun warm(text: String) {
            val work = parserScope.async { cache.parse("reply/0", text, streaming = true) }
            scheduler.runCurrent()
            runBlocking { work.await() }
        }
        warm(initial)
        rule.mainClock.autoAdvance = false
        rule.setContent {
            PersonaTheme(style = AppStyle.Brief, darkTheme = false, reduceMotion = true) {
                CompositionLocalProvider(LocalChatMarkdownCache provides cache) {
                    list = rememberChatListState()
                    val context = LocalContext.current
                    val loader = remember { ImageLoader.Builder(context).build() }
                    DisposableEffect(loader) { onDispose { loader.shutdown() } }
                    val item = Item(id, 2, role = "assistant", kind = "text", text = initial)
                    val line = remember(item) { ChatTimeline().lines(listOf(item), false, emptySet()).lines.filterIsInstance<ChatLine.Row>().last() }
                    val rows = listOf("answer") + (1..12).map { "history$it" }
                    ChatListEffects(list, rows, ready = true, onFreeze = {}, onFollow = {}, loadOlder = {})
                    ChatList(list, Modifier.testTag("chat").width(400.dp).height(650.dp).background(PersonaTheme.palette.background)) {
                        item("answer") {
                            Column(Modifier.fillMaxWidth().onSizeChanged { height = it.height }) {
                                ChatLineContent(line, SessionKey("computer", "conversation"), loader, null,
                                    onToggle = {}, onFold = {}, folding = emptySet(), foldErrors = emptyMap(), onFullText = {},
                                    style = style, textUpdates = mapOf(id to ChatTextUpdate(words, streaming)))
                            }
                        }
                        items(12, key = { "history${it + 1}" }) { n ->
                            Text("History $n. " + "Previous conversation text. ".repeat(8),
                                Modifier.fillMaxWidth().padding(Tokens.Space.m), style = PersonaTheme.type.body)
                        }
                    }
                }
            }
        }
        fun frames() { repeat(3) { rule.mainClock.advanceTimeByFrame() }; rule.waitForIdle() }
        fun read(step: Int, phase: String) {
            val row = list.list.layoutInfo.visibleItemsInfo.firstOrNull { it.key == "answer" }
            readings += "$step,$phase,$height,${list.list.firstVisibleItemIndex},${list.list.firstVisibleItemScrollOffset},${row?.offset},${row?.size}"
        }
        fun shot(name: String) {
            val bitmap = rule.onNodeWithTag("chat").captureToImage().asAndroidBitmap()
            File(output, "$name.png").outputStream().use { assertTrue(bitmap.compress(Bitmap.CompressFormat.PNG, 100, it)) }
        }
        try {
            frames(); scheduler.runCurrent(); frames()
            var expectedHeight = height
            read(0, "settled"); shot("initial")
            val deltas = listOf(
                "More words keep wrapping on the next line. ".repeat(4),
                "\n\n- First list entry\n- Second list entry\n- Third list entry",
                "\n\n```kotlin\nfun main() {\n    println(\"streamed code\")\n",
                "}\n```\n\nFinal paragraph with **bold** text and a complete ending.",
            )
            deltas.forEachIndexed { n, delta ->
                words += delta
                frames(); read(n + 1, "pending")
                if (height != expectedHeight) failures += "step ${n + 1}: pending height $height != settled $expectedHeight"
                assertEquals("bottom must follow", 0, list.list.firstVisibleItemScrollOffset)
                if (n == 2) shot("code-pending")
                scheduler.runCurrent(); frames(); read(n + 1, "parsed")
                expectedHeight = height
                if (n == 2) shot("code-parsed")
            }
            val beforeEnd = height
            streaming = false
            frames(); read(5, "ending-pending")
            scheduler.runCurrent(); frames(); read(5, "ended"); shot("ended")
            // Only the cursor disappears at completion; the Markdown tree has identical geometry.
            assertTrue("completion must not grow the row", height <= beforeEnd)
            assertTrue("final words remain present", rule.onAllNodesWithText("complete ending", substring = true).fetchSemanticsNodes().isNotEmpty())

            rule.onNodeWithTag("chat").performTouchInput { swipeDown(startY = centerY, endY = bottom - 10, durationMillis = 400) }
            frames()
            assertFalse("drag stops following", list.followsEnd)
            repeat(4) {
                rule.onNodeWithTag("chat").performTouchInput { swipeDown(startY = top + 20, endY = bottom - 20, durationMillis = 400) }
                frames()
            }
            assertTrue("reader reached history", list.list.firstVisibleItemIndex > 0)
            val anchor = list.list.firstVisibleItemIndex to list.list.firstVisibleItemScrollOffset
            words += "\n\nA later update must not pull the reader back to the bottom."
            frames(); scheduler.runCurrent(); frames(); read(6, "reading")
            assertEquals("reading anchor survives", anchor, list.list.firstVisibleItemIndex to list.list.firstVisibleItemScrollOffset)

            rule.runOnIdle { list.returnToLatest() }
            frames(); scheduler.runCurrent(); frames()
            words += " Rapid update one."
            frames()
            words += " Rapid update two."
            frames(); scheduler.runCurrent(); frames(); read(7, "rapid-updates")
            rule.onNodeWithText("Rapid update two", substring = true).assertExists()

            assertTrue(failures.joinToString("\n"), failures.isEmpty())
            passed = true
        } finally {
            File(output, "layout.csv").writeText(readings.joinToString("\n") + "\n")
            File(output, "verdict.txt").writeText(if (passed) "PASS\n" else "FAIL\n" + failures.joinToString("\n"))
            parserScope.cancel()
        }
    }
}
