package persona.core.designsystem

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(manifest = Config.NONE)
class ClipboardTest {

    private val context: Context get() = RuntimeEnvironment.getApplication()

    @Test
    fun smallTextUnderThresholdCopiesSuccessfully() {
        val clipboard = context.getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
        clipboard.clearPrimaryClip()

        val text = "Hello Persona clipboard"
        var noticeShown: String? = null

        val copied = copyTextSafely(
            context = context,
            text = text,
            label = "TestLabel",
            onNotice = { noticeShown = it },
            successNotice = "Copied!",
        )

        assertTrue(copied)
        assertEquals("Copied!", noticeShown)
        assertEquals(text, clipboard.primaryClip?.getItemAt(0)?.text?.toString())
    }

    @Test
    fun largeTextAboveThresholdIsRefusedWithNotice() {
        val clipboard = context.getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
        clipboard.clearPrimaryClip()

        // 256K + 1 characters
        val largeText = "A".repeat(MAX_CLIPBOARD_TEXT_CHARS + 1)
        var noticeShown: String? = null

        val copied = copyTextSafely(
            context = context,
            text = largeText,
            label = "TestLabel",
            onNotice = { noticeShown = it },
            successNotice = "Copied!",
            tooLargeNotice = "Text too large to copy; use Share instead",
        )

        assertFalse("Texts exceeding MAX_CLIPBOARD_TEXT_CHARS must be refused", copied)
        assertEquals("Text too large to copy; use Share instead", noticeShown)
        assertNull("Clipboard must remain empty when refused", clipboard.primaryClip)
    }

    @Test
    fun exactlyAtThresholdCopiesSuccessfully() {
        val clipboard = context.getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
        clipboard.clearPrimaryClip()

        val thresholdText = "B".repeat(MAX_CLIPBOARD_TEXT_CHARS)
        var noticeShown: String? = null

        val copied = copyTextSafely(
            context = context,
            text = thresholdText,
            label = "TestLabel",
            onNotice = { noticeShown = it },
            successNotice = "Copied!",
        )

        assertTrue(copied)
        assertEquals("Copied!", noticeShown)
        assertEquals(MAX_CLIPBOARD_TEXT_CHARS, clipboard.primaryClip?.getItemAt(0)?.text?.length)
    }

    @Test
    fun pureLengthCheckValidatesThresholdBoundary() {
        assertTrue(isClipboardTextUnderLimit(""))
        assertTrue(isClipboardTextUnderLimit("Hello"))
        assertTrue(isClipboardTextUnderLimit("A".repeat(MAX_CLIPBOARD_TEXT_CHARS)))
        assertFalse(isClipboardTextUnderLimit("A".repeat(MAX_CLIPBOARD_TEXT_CHARS + 1)))
    }
}
