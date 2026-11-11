package persona.core.designsystem

import androidx.compose.runtime.staticCompositionLocalOf

/** A button on a notice ("Undo"): its label, kept to a word or two, and what it does. */
class FeedbackAction(val label: String, val run: () -> Unit)

/** A notice as the shell shows it: the words, a button when there is one, and the [Haptic] it plays when shown. */
class FeedbackNotice(val message: String, val action: FeedbackAction? = null, val haptic: Haptic? = null)

/** Return true when the shell owns presentation, including deliberately silent refresh failures. */
interface AppFeedback {
    fun failure(error: Throwable, operation: Boolean = true): Boolean
    fun notice(message: String): Boolean
    /** A notice with a button ("Undo" after a bulk action), shown for a few seconds; false when the shell cannot show one. */
    fun notice(message: String, action: FeedbackAction): Boolean = false
}
val LocalAppFeedback = staticCompositionLocalOf<AppFeedback?> { null }

/** Keep the standard snackbar geometry, with actions as readable as its message in every interface style. */
@androidx.compose.runtime.Composable
fun JAppSnackbarHost(state: androidx.compose.material3.SnackbarHostState, modifier: androidx.compose.ui.Modifier = androidx.compose.ui.Modifier) {
    androidx.compose.material3.SnackbarHost(state, modifier) { data ->
        // The accent is not a text color on an inverse surface (Brief's can equal its background).
        androidx.compose.material3.Snackbar(data, actionColor = androidx.compose.material3.MaterialTheme.colorScheme.inverseOnSurface)
    }
}
