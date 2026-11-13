package persona.feature.chat

import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.platform.LocalSoftwareKeyboardController
import kotlinx.coroutines.delay
import persona.core.contracts.SessionKey

/**
 * The launcher's "Ask Persona" for a conversation shown inside the assistant page (#4339 C11: Android has no home page,
 * so it goes to the main conversation, as iOS's does): [session] is the conversation whose composer takes the cursor and
 * the keyboard once it shows; the chat page takes it once and sets it back to null.
 */
class ComposerFocusAsk {
    var session by mutableStateOf<SessionKey?>(null)
}

/** Provided by the assistant page around the conversation it shows; null elsewhere. */
val LocalComposerFocusAsk = staticCompositionLocalOf<ComposerFocusAsk?> { null }

/** Puts the cursor in [composer] and brings up the keyboard when [LocalComposerFocusAsk] asks it for [session]. */
@Composable
internal fun ComposerFocusAskEffect(session: SessionKey, composer: ChatComposerState?) {
    val ask = LocalComposerFocusAsk.current ?: return
    val keyboard = LocalSoftwareKeyboardController.current
    LaunchedEffect(ask, session, composer) {
        val state = composer ?: return@LaunchedEffect
        snapshotFlow { ask.session }.collect { asked ->
            if (asked != session) return@collect
            ask.session = null
            // After the page's first layout: the text field has to be attached (as the assistant page's own composer waits).
            delay(300)
            state.focusRequest++
            delay(100)
            keyboard?.show()
        }
    }
}
