package persona.feature.chat

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import persona.core.contracts.Session
import persona.core.contracts.api.ChatAction
import persona.core.designsystem.*
import persona.core.designsystem.icons.PersonaIcons

/**
 * Over the composer after a resume turned ultracode off (`ultracode_lost_at`, #582, iOS `UltracodeLostNote`): one line,
 * Turn back on (one request; the node takes the line away once it is on, "Applies next turn" while it waits) and × to
 * dismiss it (`ultracode_dismiss_lost`): gone at once, back if the hub says no. On Glass it is iOS's card (#4339 C9), without
 * the retry mark iOS puts before Turn back on: on a phone's width it cut the note's words. Brief and Board keep the plain line.
 */
@Composable
internal fun UltracodeLostNote(session: Session, state: ChatChromeState, canSend: Boolean) {
    var dismissed by remember(session.key, session.ultracodeLostAt) { mutableStateOf(false) }
    if (dismissed || !Ultracode.lostShown(session, LocalHubFeatures.current)) return
    val palette = PersonaTheme.palette
    val glass = PersonaTheme.style == AppStyle.Glass
    val frame = if (glass) Modifier.padding(horizontal = Tokens.Space.s, vertical = Tokens.Space.xs)
        .floatingSurface(RoundedCornerShape(Tokens.Radius.card)).padding(start = Tokens.Space.m)
        else Modifier.padding(horizontal = Tokens.Space.m, vertical = Tokens.Space.xs)
    Row(Modifier.fillMaxWidth().then(frame),
        horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s), verticalAlignment = Alignment.CenterVertically) {
        JIcon(PersonaIcons.workflow, null, size = Tokens.Size.iconXS, tint = palette.ai)
        Text(stringResource(R.string.chat_ultracode_lost), Modifier.weight(1f), style = PersonaTheme.type.callout, color = palette.ink2,
            maxLines = 1, overflow = TextOverflow.Ellipsis)
        if (session.ultracodePending) Text(stringResource(R.string.chat_ultracode_pending), style = PersonaTheme.type.callout, color = palette.ink2, maxLines = 1)
        else JTextButton(stringResource(R.string.chat_ultracode_turn_back_on), { state.run(session, ChatAction.Modes(ultracode = true)) },
            enabled = canSend && !state.busy)
        JIconButton(PersonaIcons.close, stringResource(R.string.chat_ultracode_lost_dismiss), {
            dismissed = true
            state.run(session, ChatAction.Modes(dismissUltracodeLost = true)) { done -> if (!done) dismissed = false }
        }, enabled = !state.busy)
    }
}
