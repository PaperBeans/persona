package persona.core.designsystem

import java.util.Locale
import org.junit.Assert.assertEquals
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

// A chart's short numbers in the app's language (#4149): English keeps its K / M / B / T exactly as before; Chinese and
// Japanese count in ten-thousands through the system's ICU (android.icu, so on Robolectric).
@RunWith(RobolectricTestRunner::class)
@Config(manifest = Config.NONE, sdk = [35])
class ChartCompactLocaleTest {
    private val values = listOf(999.0, 1_000.0, 12_345.0, 1_200_000.0, 1_200_000_000.0, 1.2e12, -12_345.0, -1_200_000_000.0)

    private fun compact(locale: Locale) = values.map { ChartMath.compact(it, locale) }

    @Test fun englishIsUnchanged() {
        val english = listOf("999", "1,000", "12.3K", "1.2M", "1.2B", "1.2T", "-12.3K", "-1.2B")
        assertEquals(english, compact(Locale.ENGLISH))
        assertEquals(english, compact(Locale.US))
        // the default is English, as every caller had before
        assertEquals(english, values.map { ChartMath.compact(it) })
        // a language with no ten-thousands keeps the English letters
        assertEquals(english, compact(Locale.FRENCH))
    }

    @Test fun chineseCountsInWanAndYi() {
        val chinese = listOf("999", "1000", "1.2万", "120万", "12亿", "1.2万亿", "-1.2万", "-12亿")
        assertEquals(chinese, compact(Locale.SIMPLIFIED_CHINESE))
        assertEquals(chinese, compact(Locale.forLanguageTag("zh-Hans")))
    }

    // The app's Chinese is Simplified only (#4161): a Traditional-region phone's locale still writes 万 / 亿, not 萬 / 億.
    @Test fun traditionalRegionsStaySimplified() {
        val chinese = listOf("999", "1000", "1.2万", "120万", "12亿", "1.2万亿", "-1.2万", "-12亿")
        for (tag in listOf("zh-TW", "zh-HK", "zh-Hant", "zh-Hant-TW", "zh-Hant-HK")) {
            assertEquals(tag, chinese, compact(Locale.forLanguageTag(tag)))
        }
    }

    @Test fun japaneseCountsInManAndOku() {
        assertEquals(listOf("999", "1000", "1.2万", "120万", "12億", "1.2兆", "-1.2万", "-12億"), compact(Locale.JAPANESE))
    }

    @Test fun notANumberIsADash() {
        assertEquals("—", ChartMath.compact(Double.NaN, Locale.SIMPLIFIED_CHINESE))
        assertEquals("—", ChartMath.compact(Double.POSITIVE_INFINITY, Locale.JAPANESE))
    }
}
