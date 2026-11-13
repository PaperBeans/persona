package persona.feature.terminal

import android.graphics.Bitmap
import androidx.activity.ComponentActivity
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Text
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTouchInput
import androidx.compose.ui.test.swipe
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.*
import persona.core.contracts.api.*
import persona.core.designsystem.*
import java.io.File

/**
 * The terminal over its chat (#4339 C7, iOS `TerminalCover`): the release rule, Close, Back and the bar's drag, and the
 * cover over a page in every style, light and dark. PNGs in `build/terminal-cover-shots/` (or `-Dpersona.shots=<dir>`).
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h800dp-xhdpi")
class TerminalCoverTest {
    @get:Rule val rule = createAndroidComposeRule<ComponentActivity>()

    @Test fun letGoFarOrFlungItCloses() {
        val at = 140f
        assertFalse(coverCloses(100f, 0f, at))
        assertTrue(coverCloses(141f, 0f, at))
        // iOS's predicted end past 420 pt: a short fast flick closes, a short slow one springs back.
        assertTrue(coverCloses(40f, 1_000f, at))
        assertFalse(coverCloses(40f, 300f, at))
        // Pushed back up: never closes.
        assertFalse(coverCloses(100f, -2_000f, at))
    }

    /** A node that only has the screen read (`GET …/screen`): watched, read once a second. */
    private class Screen : TerminalTransport {
        override val connection = MutableStateFlow(ConnectionState.Online)
        override val features = MutableStateFlow(emptySet<String>())
        override val events = MutableSharedFlow<ServerEvent>()
        override fun subscribe(key: SessionKey, epoch: String?, after: Int, size: TerminalSize?, takeover: Boolean) = false
        override fun unsubscribe(key: SessionKey) = Unit
        override fun input(key: SessionKey, text: String, size: TerminalSize?, scroll: Int, at: TerminalCellPosition?, click: TerminalCellPosition?) = false
        override suspend fun screen(key: SessionKey) = TerminalScreen("$ npm test\n\n> app@1.0.0 test\n> vitest run\n\n ✓ src/app.test.ts (12 tests) 48ms\n\n Test Files  1 passed (1)\n      Tests  12 passed (12)\n$ ")
        override suspend fun keys(key: SessionKey, keys: List<String>, text: String) = Unit
    }

    private var closes = 0
    private var open by mutableStateOf(true)
    private var style by mutableStateOf(AppStyle.Glass)
    private var dark by mutableStateOf(false)

    private fun cover() {
        val model = TerminalViewModel(SessionKey("lab", "s1"), Screen())
        rule.mainClock.autoAdvance = false
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                Box(Modifier.fillMaxSize().background(PersonaTheme.palette.background).testTag("shot")) {
                    // What the cover goes over: the chat it came from.
                    Column(Modifier.padding(pagePadding)) {
                        repeat(12) { Text("The chat under the terminal, line ${it + 1}", color = PersonaTheme.palette.ink) }
                    }
                    TerminalCoverFrame(open, { closes++; open = false }) { grab -> TerminalPage(model, { closes++; open = false }, "s1", grab) }
                }
            }
        }
        settle()
    }

    /** The paused clock recomposes only for changes already applied (a state the test set). */
    private fun settle() {
        Snapshot.sendApplyNotifications()
        repeat(8) { rule.mainClock.advanceTimeBy(250); Thread.sleep(20) }
        rule.mainClock.advanceTimeByFrame()
    }

    private fun drag(distance: Float, millis: Long) {
        rule.onNodeWithTag("terminal_bar").performTouchInput {
            val from = Offset(width * 0.3f, height / 2f)
            swipe(from, from + Offset(0f, distance * density), millis)
        }
        settle()
    }

    @Test fun closeAndBackCloseIt() {
        cover()
        rule.onNodeWithText("Updates every second").assertExists()
        rule.onNodeWithContentDescription("Close terminal").performClick()
        settle()
        assertEquals(1, closes)
        rule.onNodeWithTag("terminal_cover").assertDoesNotExist()
        open = true
        settle()
        rule.onNodeWithTag("terminal_cover").assertExists()
        rule.activity.onBackPressedDispatcher.onBackPressed()
        settle()
        assertEquals(2, closes)
        rule.onNodeWithTag("terminal_cover").assertDoesNotExist()
    }

    @Test fun theBarDragsItDown() {
        cover()
        drag(60f, 1_500) // short and slow: it springs back
        assertEquals(0, closes)
        rule.onNodeWithTag("terminal_cover").assertExists()
        drag(220f, 2_000) // past 140 dp
        assertEquals(1, closes)
        open = true
        settle()
        drag(80f, 40) // a flick
        assertEquals(2, closes)
    }

    @Test fun overAPageInEveryStyle() {
        cover()
        val out = File(System.getProperty("persona.shots") ?: "build/terminal-cover-shots").also { it.mkdirs() }
        for (s in AppStyle.entries) for (d in listOf(false, true)) {
            style = s; dark = d
            settle()
            rule.onNodeWithText("Updates every second").assertExists()
            fun shot(name: String) {
                val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
                File(out, "terminal-cover-${s.name.lowercase()}-${if (d) "dark" else "light"}$name.png").outputStream()
                    .use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
            }
            shot("")
            // Held part way down: the chat shows through, dimmed less.
            rule.onNodeWithTag("terminal_bar").performTouchInput { down(Offset(width * 0.3f, height / 2f)); moveBy(Offset(0f, 120 * density)) }
            settle()
            shot("-dragged")
            rule.onNodeWithTag("terminal_bar").performTouchInput { up() }
            settle()
            assertEquals(0, closes)
        }
    }
}
