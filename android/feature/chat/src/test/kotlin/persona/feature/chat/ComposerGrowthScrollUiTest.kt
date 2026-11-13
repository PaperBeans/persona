package persona.feature.chat

import android.content.res.Configuration
import android.graphics.Bitmap
import android.graphics.Canvas
import android.view.View
import androidx.activity.ComponentActivity
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.text.TextLayoutResult
import androidx.compose.ui.text.TextRange
import androidx.compose.ui.text.input.TextFieldValue
import androidx.compose.ui.unit.Density
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.SessionKey
import persona.core.designsystem.*
import java.io.File

/** Native JVM Compose evidence for draft growth and manual scrolling; no emulator or IME claims. */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "zh-w400dp-h640dp-xhdpi")
class ComposerGrowthScrollUiTest {
    @get:Rule val rule = createAndroidComposeRule<ComponentActivity>()
    private val state = ChatComposerState()
    private val out = File("build/composer-growth-scroll-shots").apply { mkdirs() }
    private var style by mutableStateOf(AppStyle.Glass)
    private var dark by mutableStateOf(false)
    private var scale by mutableStateOf(1f)
    private var height by mutableStateOf(640)
    private var home by mutableStateOf(false)
    private fun draft(lines: Int) = (1..lines).joinToString("\n") { "Line $it: draft text" }
    private fun field(expanded: Boolean = false) = rule.onNode(hasSetTextAction() and
        if (expanded) hasAnyAncestor(hasTestTag("composer-expanded")) else !hasAnyAncestor(hasTestTag("composer-expanded")))
    private fun mount() {
        rule.setContent {
            val density = LocalDensity.current
            val configuration = Configuration(LocalConfiguration.current).apply {
                screenHeightDp = height
                fontScale = scale
                uiMode = (uiMode and Configuration.UI_MODE_NIGHT_MASK.inv()) or
                    if (dark) Configuration.UI_MODE_NIGHT_YES else Configuration.UI_MODE_NIGHT_NO
            }
            CompositionLocalProvider(LocalConfiguration provides configuration,
                LocalDensity provides Density(density.density, scale),
                LocalHomeChrome provides if (home) HomeChrome(items = listOf(NavItem("assistant", "Assistant", persona.core.designsystem.icons.PhIcons.House)),
                    selected = "assistant", onSelect = {}, leading = {}) else null) {
                PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                    Box(Modifier.width(400.dp).height(height.dp).testTag("shot").background(PersonaTheme.palette.background)) {
                        Column(Modifier.fillMaxSize()) {
                            Spacer(Modifier.weight(1f))
                            ChatComposer(SessionKey("example-computer", "example-session"), state,
                                working = false, sending = false, canClear = true, service = null,
                                onSend = {}, onCommand = {}, onNotice = {})
                        }
                    }
                }
            }
        }
    }
    private fun bitmap() = field().captureToImage().asAndroidBitmap()
    private fun save(name: String, bitmap: Bitmap) {
        File(out, "$name.png").outputStream().use { assertTrue(bitmap.compress(Bitmap.CompressFormat.PNG, 100, it)) }
    }
    private fun scene(name: String) {
        rule.waitForIdle()
        save("$name-${style.name.lowercase()}-${if (dark) "dark" else "light"}-${if (scale == 1f) "standard" else "large"}",
            rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap())
    }
    private fun layout(expanded: Boolean = false): TextLayoutResult {
        val layouts = mutableListOf<TextLayoutResult>()
        field(expanded).performSemanticsAction(SemanticsActions.GetTextLayoutResult) { assertTrue(it(layouts)) }
        return layouts.single()
    }
    @Test
    @Config(qualifiers = "zh-w400dp-h1000dp-xhdpi")
    fun growsThroughSixLinesInShortAndTallWindowsAndEveryStyle() {
        mount()
        for (s in AppStyle.entries) for (h in listOf(400, 500, 640, 1000)) for (isHome in listOf(false, true)) {
            rule.runOnIdle { style = s; height = h; home = isHome }
            val heights = (1..8).map { lines ->
                rule.runOnIdle { state.value = TextFieldValue(draft(lines)) }
                rule.waitForIdle()
                field().fetchSemanticsNode().boundsInRoot.height
            }
            println("GROW style=$s viewport=${h}dp home=$isHome heights=$heights")
            for (line in 1..5) assertTrue("$s ${h}dp line ${line + 1}: $heights", heights[line] > heights[line - 1])
            assertEquals("Seven stops at six: $heights", heights[5], heights[6], 1f)
            assertEquals("Eight stops at six: $heights", heights[5], heights[7], 1f)
            rule.onNodeWithContentDescription("发送").assertIsDisplayed()
        }
    }
    @Test fun wrappedDraftAndLargeTextStillUseSixVisibleLines() {
        mount()
        for (s in AppStyle.entries) for (fontScale in listOf(1f, 2f)) {
            rule.runOnIdle { style = s; scale = fontScale; height = 500 }
            rule.runOnIdle { state.value = TextFieldValue(List(6) { "Word" }.joinToString("\n")) }
            rule.waitForIdle()
            val sixLineHeight = field().fetchSemanticsNode().boundsInRoot.height
            rule.runOnIdle { state.value = TextFieldValue("Word ".repeat(180)) }
            rule.waitForIdle()
            val layout = layout()
            val fieldHeight = field().fetchSemanticsNode().boundsInRoot.height
            println("WRAP style=$s scale=$fontScale lines=${layout.lineCount} height=$fieldHeight sixLines=$sixLineHeight")
            assertTrue(layout.lineCount > 6)
            // Use the real six-line field: a long paragraph's intermediate line bottom includes line spacing
            // that the clipped field's last line does not, notably in Brief and Board's bundled fonts.
            assertEquals(sixLineHeight, fieldHeight, 1f)
            rule.onNodeWithContentDescription("展开输入框").assertIsDisplayed()
        }
    }
    @Test fun manualScrollRemainsIdleAndInputRevealsCaret() {
        mount()
        field().performClick()
        // Render the line prefixes once as native pixel templates, so the test can identify visible rows without OCR.
        val prefixWidth = 120
        val templates = (1..30).associateWith { line ->
            field().performTextReplacement("Line $line: draft text")
            rule.waitForIdle()
            bitmap().let { Bitmap.createBitmap(it, 0, 0, prefixWidth, it.height) }
        }
        fun visibleLines(image: Bitmap): List<Int> = templates.mapNotNull { (line, template) ->
            (0..image.height - template.height).firstOrNull { y ->
                Bitmap.createBitmap(image, 0, y, prefixWidth, template.height).sameAs(template)
            }?.let { y -> line to y }
        }.sortedBy { it.second }.map { it.first }
        val text = draft(30)
        field().performTextReplacement(text)
        field().performTextInputSelection(TextRange(text.length))
        rule.waitForIdle()
        val before = bitmap()
        assertEquals((25..30).toList(), visibleLines(before))
        save("before-scroll", before)
        field().performTouchInput { swipeDown(durationMillis = 600) }
        rule.waitForIdle()
        val manual = bitmap()
        val earlier = visibleLines(manual)
        println("SCROLL before=${visibleLines(before)} manual=$earlier")
        assertTrue("The real swipe must reveal earlier rows: $earlier", earlier.size >= 5 && earlier.last() < 30)
        save("manual-scroll", manual)
        repeat(20) {
            rule.mainClock.advanceTimeBy(250)
            rule.waitForIdle()
            assertTrue("Idle frame $it must preserve the draft scroll", manual.sameAs(bitmap()))
        }
        println("SCROLL idle5000ms=${visibleLines(bitmap())}; identical native pixels across 20 idle samples")
        save("idle-scroll", bitmap())
        field().performTextInput("X")
        rule.waitForIdle()
        val afterInput = bitmap()
        assertEquals((25..30).toList(), visibleLines(afterInput))
        save("after-input", afterInput)
        field().performTouchInput { swipeDown(durationMillis = 600) }
        rule.waitForIdle()
        assertTrue(visibleLines(bitmap()).last() < 30)
        field().performTextInputSelection(TextRange(text.length - 1))
        rule.waitForIdle()
        assertEquals((25..30).toList(), visibleLines(bitmap()))
        save("after-selection", bitmap())
        assertEquals(text + "X", state.value.text)
        println("SCROLL input=${visibleLines(afterInput)} selection=${visibleLines(bitmap())}")
    }
    @Test fun lightAndDarkEvidenceAndExpandedControlsInspectionOnly() = screenshotScenes(1f)
    @Test fun largeTextLightAndDarkEditorInspectionOnly() = screenshotScenes(2f)

    private fun screenshotScenes(fontScale: Float) {
        // Font-scale changes recreate the Activity; complete that before installing the test composition.
        org.robolectric.RuntimeEnvironment.setFontScale(fontScale)
        rule.activityRule.scenario.recreate()
        scale = fontScale
        mount()
        for (s in AppStyle.entries) for (d in listOf(false, true)) {
            rule.runOnIdle { style = s; dark = d; height = 640; home = true }
            for (lines in if (fontScale == 1f) listOf(1, 6, 7) else listOf(6)) {
                rule.runOnIdle { state.value = TextFieldValue((1..lines).joinToString("\n") { "Line $it" }) }
                rule.waitForIdle()
                if (lines <= 6) rule.onNodeWithContentDescription("展开输入框").assertDoesNotExist()
                else rule.onNodeWithContentDescription("展开输入框").assertIsDisplayed()
                scene("compact-$lines-lines")
            }
            rule.runOnIdle { state.value = TextFieldValue(draft(30), TextRange(0)) }
            rule.waitForIdle()
            rule.onNodeWithContentDescription("展开输入框").performClick()
            rule.waitForIdle()
            val collapse = rule.onNodeWithTag("composer-collapse").fetchSemanticsNode().boundsInRoot
            val textBounds = field(expanded = true).fetchSemanticsNode().boundsInRoot
            assertTrue("Expanded text and top control occupy separate rows", collapse.bottom <= textBounds.top)
            val editorLayout = layout(expanded = true)
            val editorLineHeight = editorLayout.getLineBottom(0) - editorLayout.getLineTop(0)
            assertEquals("The dialog must really use the requested system font scale", fontScale,
                editorLayout.layoutInput.density.fontScale, 0f)
            println("EXPANDED style=$s dark=$d scale=$fontScale lineHeight=$editorLineHeight collapse=$collapse text=$textBounds")
            captureEditor("expanded-${s.name.lowercase()}-${if (d) "dark" else "light"}-${if (fontScale == 1f) "standard" else "large"}")
            rule.onNodeWithTag("composer-collapse").performClick()
            rule.waitForIdle()
        }
    }
    /** Draw the actual dialog window; captureToImage alone can crop the Activity behind it in Robolectric. */
    @Suppress("UNCHECKED_CAST")
    private fun captureEditor(name: String) {
        val image = rule.runOnIdle {
            val global = Class.forName("android.view.WindowManagerGlobal")
            val instance = global.getMethod("getInstance").invoke(null)
            val views = global.getDeclaredField("mViews").apply { isAccessible = true }.get(instance) as List<View>
            assertTrue("The editor must have a real dialog window", views.size > 1)
            val editor = views.last()
            Bitmap.createBitmap(editor.width, editor.height, Bitmap.Config.ARGB_8888).also { editor.draw(Canvas(it)) }
        }
        save(name, image)
    }
}
