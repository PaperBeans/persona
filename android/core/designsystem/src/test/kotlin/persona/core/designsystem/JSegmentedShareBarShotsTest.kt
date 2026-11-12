package persona.core.designsystem

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import java.io.File

/**
 * A stacked bar of one category (a persona-card's single day, #5386) is a horizontal segmented bar, while a stacked
 * bar with several categories keeps its columns. Both drawn on the JVM with Robolectric's native graphics, every
 * style, light and dark; the PNGs go to `build/share-bar-shots/` (or `-Dpersona.shots=<dir>`) to look at.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "zh-w400dp-h900dp-xhdpi")
class JSegmentedShareBarShotsTest {
    @get:Rule val rule = createComposeRule()

    private fun series(name: String, values: List<Double>) =
        JChartSeries(name, values, values.map { "$it kcal" })

    // one category: every series has the single day's one point
    private val oneDay = JChartData(
        JChartKind.Bar, listOf("今天"),
        listOf(series("午餐", listOf(900.0)), series("加餐", listOf(150.0)), JChartSeries("还能吃", listOf(900.0), listOf("900.0 kcal"), Tone.Neutral)),
        stacked = true,
    )

    // two categories: the multi-day comparison stays columns
    private val twoDays = JChartData(
        JChartKind.Bar, listOf("周一", "周二"),
        listOf(series("午餐", listOf(800.0, 700.0)), series("加餐", listOf(60.0, 90.0))),
        stacked = true,
    )

    @Test fun oneCategoryIsAHorizontalBarAndSeveralStayColumns() {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                Column(Modifier.testTag("shot").width(400.dp).background(PersonaTheme.palette.background).padding(Tokens.Space.l)) {
                    JChart(oneDay, title = "今日热量", detail = "单类目：横向分段条")
                    JChart(twoDays, Modifier.padding(top = Tokens.Space.l), title = "近两天热量对比", detail = "多类目：竖柱")
                }
            }
        }
        val out = File(System.getProperty("persona.shots") ?: "build/share-bar-shots").also { it.mkdirs() }
        for (s in AppStyle.entries) for (d in listOf(false, true)) {
            rule.runOnIdle { style = s; dark = d }
            rule.waitForIdle()
            val name = "share-bar-${s.name.lowercase()}-${if (d) "dark" else "light"}"
            val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
            val background = bitmap.getPixel(1, 1)
            var other = 0
            for (y in 0 until bitmap.height step 7) for (x in 0 until bitmap.width step 7) if (bitmap.getPixel(x, y) != background) other++
            assertTrue("$name drew nothing", other > 100)
            File(out, "$name.png").outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
        }
    }
}
