package persona.core.designsystem

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context

/**
 * Upper bound for text placed directly on the system clipboard.
 *
 * Android binder transactions are limited to ~1 MB for the process. Above that, setPrimaryClip
 * throws a RuntimeException wrapping TransactionTooLargeException. A 256K-character bound
 * leaves ample headroom under the IPC limit across all multi-byte encodings while holding any
 * realistic snippet.
 */
const val MAX_CLIPBOARD_TEXT_CHARS = 256 * 1024

/**
 * Checks whether [text] length is within [MAX_CLIPBOARD_TEXT_CHARS] safe threshold.
 */
fun isClipboardTextUnderLimit(text: String): Boolean = text.length <= MAX_CLIPBOARD_TEXT_CHARS

/**
 * Places [text] on the system clipboard if its length is within [MAX_CLIPBOARD_TEXT_CHARS],
 * notifying [onNotice] or [feedback] of success or refusal.
 *
 * If [text] exceeds [MAX_CLIPBOARD_TEXT_CHARS], copying is refused and [tooLargeNotice] (or the
 * localized `designsystem_copy_too_large`) is shown to direct the user to Share instead.
 *
 * Any runtime failure from the system clipboard service is caught, preventing
 * [android.os.TransactionTooLargeException] crashes.
 *
 * @return true if the clip was successfully placed on the clipboard.
 */
fun copyTextSafely(
    context: Context,
    text: String,
    label: String = "Persona",
    feedback: AppFeedback? = null,
    onNotice: ((String) -> Unit)? = null,
    successNotice: String? = null,
    tooLargeNotice: String? = null,
): Boolean {
    val notify = onNotice ?: { msg -> feedback?.notice(msg) }
    if (text.length > MAX_CLIPBOARD_TEXT_CHARS) {
        val notice = tooLargeNotice ?: context.getString(R.string.designsystem_copy_too_large)
        notify(notice)
        return false
    }
    return try {
        val clipboard = context.getSystemService(Context.CLIPBOARD_SERVICE) as? ClipboardManager
        if (clipboard != null) {
            clipboard.setPrimaryClip(ClipData.newPlainText(label, text))
            if (successNotice != null) {
                notify(successNotice)
            }
            true
        } else {
            false
        }
    } catch (_: Exception) {
        val notice = tooLargeNotice ?: context.getString(R.string.designsystem_copy_too_large)
        notify(notice)
        false
    }
}
