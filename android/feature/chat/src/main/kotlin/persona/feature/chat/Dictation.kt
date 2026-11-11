package persona.feature.chat

import android.app.Activity
import android.content.Intent
import android.speech.RecognizerIntent
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.text.TextRange
import androidx.compose.ui.text.input.TextFieldValue

/**
 * Voice input (iOS Dictation): the phone's own voice input screen (RecognizerIntent), so it works with whichever recognizer
 * the phone has (Xiaomi's, Google's…) and needs no Google services; the app neither records nor asks for the microphone.
 * What was heard goes to [onHeard]. Null when the phone has no recognizer: the button is not shown at all. Android 11+
 * only finds one because the module's manifest declares the RECOGNIZE_SPEECH query.
 */
@Composable
fun rememberDictation(onHeard: (String) -> Unit, onError: (String) -> Unit, prompt: String? = null): (() -> Unit)? {
    val context = LocalContext.current
    val resources = LocalResources.current
    // Also the to-do notes' mic (#4636), with its own [prompt].
    val intent = remember(resources, prompt) {
        Intent(RecognizerIntent.ACTION_RECOGNIZE_SPEECH)
            .putExtra(RecognizerIntent.EXTRA_LANGUAGE_MODEL, RecognizerIntent.LANGUAGE_MODEL_FREE_FORM)
            .putExtra(RecognizerIntent.EXTRA_LANGUAGE, resources.configuration.locales[0].let { if (it.language == "zh") "zh-CN" else it.toLanguageTag() })
            .putExtra(RecognizerIntent.EXTRA_PROMPT, prompt ?: resources.getString(R.string.chat_dictation_prompt))
    }
    val available = remember(context) { runCatching { intent.resolveActivity(context.packageManager) != null }.getOrDefault(false) }
    val heard by rememberUpdatedState(onHeard)
    val failed by rememberUpdatedState(onError)
    val launcher = rememberLauncherForActivityResult(ActivityResultContracts.StartActivityForResult()) { result ->
        if (result.resultCode == Activity.RESULT_OK) result.data?.getStringArrayListExtra(RecognizerIntent.EXTRA_RESULTS)
            ?.firstOrNull()?.trim()?.takeIf(String::isNotEmpty)?.let(heard)
    }
    if (!available) return null
    return remember(launcher, intent) { { runCatching { launcher.launch(intent) }.onFailure { failed(resources.getString(R.string.chat_dictation_unavailable)) } } }
}

/**
 * What dictation heard, at the end of the draft: right after it, with one space only between Latin text and a Latin
 * start ("run the tests" + "and commit"); Chinese needs none, and spaces the draft ended with are dropped.
 */
fun withDictation(draft: String, heard: String): String {
    if (draft.isBlank()) return heard
    if (heard.isEmpty()) return draft
    val text = draft.trimEnd()
    val last = text.last()
    val separator = if (last.code < 128 && last.isLetterOrDigit() && heard.first().code < 128) " " else ""
    return text + separator + heard
}

/** Puts what dictation heard at the end of the draft, the cursor after it; never at the selection (no padding spaces). */
internal fun ChatComposerState.appendDictation(heard: String) {
    beginEdit()
    val text = withDictation(value.text, heard)
    value = TextFieldValue(text, TextRange(text.length))
}
