package persona.feature.terminal

import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import persona.core.designsystem.*
import persona.core.designsystem.icons.PhIcons
import persona.core.terminal.TerminalText

/**
 * "Select text": the terminal (the rows kept while driving it, then the screen) as plain text over the whole screen. Rows
 * stay rows (monospaced, never folded, scrolled both ways) so columns and frames line up as on screen; a long press
 * selects any part, "Copy all" copies everything. iOS selects on the grid itself; Android borrows a text page, as the old
 * Android app did.
 */
@Composable
internal fun TerminalSelectText(text: String, onDismiss: () -> Unit) {
    val context = LocalContext.current
    val feedback = LocalAppFeedback.current
    val palette = PersonaTheme.palette
    val clipLabel = stringResource(R.string.terminal_clip_label)
    val copied = stringResource(R.string.terminal_copied)
    JFullScreen(onDismiss) {
        JPage(stringResource(R.string.terminal_select_text), onBack = onDismiss, actions = {
            JTextButton(stringResource(R.string.terminal_copy_all), {
                copyTextSafely(context = context, text = text, label = clipLabel, feedback = feedback, successNotice = copied)
                onDismiss()
            }, enabled = text.isNotEmpty())
        }) { padding ->
            if (text.isEmpty()) Box(Modifier.fillMaxSize().padding(padding), contentAlignment = Alignment.Center) {
                JEmptyState(stringResource(R.string.terminal_select_empty_title), stringResource(R.string.terminal_select_empty_body), icon = PhIcons.TerminalWindow)
            } else SelectionContainer(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).horizontalScroll(rememberScrollState())) {
                // Shown with glyphs the monospaced face has (⏵ was an empty box); "Copy all" copies the terminal's own text.
                val shown = remember(text) { TerminalText.readable(text) }
                Text(shown, style = PersonaTheme.type.code, color = palette.ink, softWrap = false,
                    modifier = Modifier.padding(start = pagePadding, end = pagePadding,
                        top = padding.calculateTopPadding() + Tokens.Space.s, bottom = padding.calculateBottomPadding() + Tokens.Space.l))
            }
        }
    }
}
