package persona.core.designsystem

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.unit.dp
import persona.core.domain.sort.AgentRowStyle
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

/**
 * A session row that is muted and then unmuted draws exactly what it drew before: the board strip its plate, holder
 * and border, glass and brief the hairline above the row (the fade is a [stableAlpha] layer, as in EnabledStateTest).
 * Rendered with Robolectric's native graphics, one pixel per dp. Each row is compared with itself: rows at different
 * heights anti-alias their glyphs differently.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "zh-w400dp-h900dp-mdpi")
class AgentRowTest {
    @get:Rule val rule = createComposeRule()

    private val tags = AppStyle.entries.flatMap { style -> listOf(false, true).map { dark -> style to "$style dark=$dark" } }

    /** A real session (a finished one): muting changes its mark and colours as well as the fade. */
    @Test fun rowDrawsTheSameOnceUnmuted() {
        var muted by mutableStateOf(false)
        rows { style, dark ->
            AgentRow(
                "补齐网络层的单元测试", Status.Idle, onClick = {}, Modifier.testTag("$style dark=$dark").width(320.dp),
                onLongClick = {}, depth = 1, position = GroupPosition(first = false, last = true),
                preview = "可以按食材和用时筛选了", time = "2 分钟前", muted = muted,
            )
        }
        val before = tags.associate { (_, tag) -> tag to pixels(tag) }
        muted = true
        rule.waitForIdle()
        tags.forEach { (_, tag) -> assertFalse("muted $tag fades", before.getValue(tag).contentEquals(pixels(tag))) }
        muted = false
        rule.waitForIdle()
        tags.forEach { (_, tag) -> assertTrue("unmuted $tag draws as before", before.getValue(tag).contentEquals(pixels(tag))) }
    }

    /**
     * Only the fade changes (the row's own status is the muted one, so its colours stay and nothing else redraws what
     * sits behind the content): unmuted, the board strip's plate and holder and the hairline above glass and brief rows
     * are drawn (sampled where nothing else is) and the row matches what it drew before it was muted.
     */
    @Test fun plateAndHairlineStayWhenOnlyTheFadeChanges() {
        var muted by mutableStateOf(false)
        rows { style, dark ->
            AgentRow(
                "清理旧的检查点", Status.Muted, onClick = {}, Modifier.testTag("$style dark=$dark").width(320.dp),
                position = GroupPosition(first = false, last = true), time = "2 小时前", compact = true, muted = muted,
            )
        }
        val before = tags.associate { (_, tag) -> tag to pixels(tag) }
        muted = true
        rule.waitForIdle()
        muted = false
        rule.waitForIdle()
        tags.forEach { (style, tag) ->
            val bitmap = rule.onNodeWithTag(tag).captureToImage().asAndroidBitmap()
            // Board: the holder's left edge (x 4) and the plate between holder and title (x 30), half way down.
            // Glass and brief: the hairline along the top edge, right of where the title starts.
            val probes = if (style == AppStyle.Board) listOf(4 to bitmap.height / 2, 30 to bitmap.height / 2) else listOf(120 to 0)
            probes.forEach { (x, y) -> assertNotEquals("unmuted $tag draws at ($x, $y)", Backdrop.toArgb(), bitmap.getPixel(x, y)) }
            assertTrue("unmuted $tag draws as before", before.getValue(tag).contentEquals(pixels(tag)))
        }
    }

    /** Rows with sideUnread cleared draw pixel-identically to rows that never had one, across every style and theme. */
    @Test fun rowWithoutUnreadSideDrawsPixelIdenticallyAndShowsAcrossAllRowStyles() {
        var sideUnread by mutableStateOf<RowSideUnread?>(null)
        rows { style, dark ->
            Column {
                AgentRow(
                    "Review PR", Status.Unread, onClick = {}, Modifier.testTag("$style dark=$dark").width(320.dp),
                    preview = "Parent summary", time = "2m", sideUnread = sideUnread,
                )
                if (style == AppStyle.Glass && !dark) {
                    for (rowStyle in AgentRowStyle.entries) {
                        AgentRow(
                            "Review $rowStyle", Status.Unread, onClick = {}, Modifier.testTag("glass-$rowStyle").width(320.dp),
                            style = rowStyle, sideUnread = sideUnread,
                        )
                    }
                }
            }
        }
        val baseline = tags.associate { (_, tag) -> tag to pixels(tag) }
        val glassBaseline = AgentRowStyle.entries.associateWith { pixels("glass-$it") }

        sideUnread = RowSideUnread(label = "New side reply: Side chat", more = "+1", onClick = {})
        rule.waitForIdle()
        tags.forEach { (_, tag) -> assertFalse("with sideUnread $tag changes", baseline.getValue(tag).contentEquals(pixels(tag))) }
        AgentRowStyle.entries.forEach { rowStyle ->
            assertFalse("with sideUnread Glass $rowStyle changes", glassBaseline.getValue(rowStyle).contentEquals(pixels("glass-$rowStyle")))
        }

        sideUnread = null
        rule.waitForIdle()
        tags.forEach { (_, tag) -> assertTrue("cleared $tag is pixel-identical", baseline.getValue(tag).contentEquals(pixels(tag))) }
        AgentRowStyle.entries.forEach { rowStyle ->
            assertTrue("cleared Glass $rowStyle is pixel-identical", glassBaseline.getValue(rowStyle).contentEquals(pixels("glass-$rowStyle")))
        }
    }

    /** One row per style and theme on a backdrop no style uses. */
    private fun rows(row: @Composable (AppStyle, Boolean) -> Unit) {
        rule.setContent {
            Column(Modifier.background(Backdrop)) {
                for (style in AppStyle.entries) for (dark in listOf(false, true)) {
                    PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) { row(style, dark) }
                }
            }
        }
        rule.waitForIdle()
    }

    private fun pixels(tag: String): IntArray {
        val bitmap = rule.onNodeWithTag(tag).captureToImage().asAndroidBitmap()
        return IntArray(bitmap.width * bitmap.height).also { bitmap.getPixels(it, 0, bitmap.width, 0, 0, bitmap.width, bitmap.height) }
    }

    private companion object {
        /** Not a colour of any style: a plate or line that is not drawn shows it. */
        val Backdrop = Color(0xFFFF00FF)
    }
}
