package persona.feature.chat

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import persona.core.contracts.Interaction
import persona.core.designsystem.DecisionButtons
import persona.core.designsystem.JAction
import persona.core.designsystem.JCodeBlock
import persona.core.designsystem.JNotice
import persona.core.designsystem.JSecretField
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.Status
import persona.core.designsystem.Tokens
import persona.core.designsystem.icons.PersonaIcons

/**
 * A password card's body (#1137), in the chat and in the Agents tab: why the password is needed and every command
 * exactly as it will run, read-only. The computer is in the card's headline.
 */
@Composable
fun SecretRunSummary(request: Interaction) {
    val run = request.secretRun ?: return
    Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
        if (run.commands.isEmpty()) {
            // No readable commands in tool_input: the card's summary, which repeats the computer, the purpose and every
            // command (as iOS), so Run never sits under an empty list.
            request.summary?.takeIf(String::isNotBlank)?.let { JCodeBlock(it) }
        } else {
            if (run.purpose.isNotEmpty()) Text(run.purpose, style = PersonaTheme.type.body)
            Text(stringResource(R.string.chat_request_secret_run_commands), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2)
            // One block per command: a long one that wraps never reads as two.
            run.commands.forEach { JCodeBlock(it) }
        }
        if (run.delivery == "env") {
            JNotice(stringResource(R.string.chat_request_secret_run_env_warning), status = Status.NeedsYou, icon = PersonaIcons.error)
        }
    }
}

/**
 * A password card's answers (#1581), in the chat and in the Agents tab: the password field, how the password is used,
 * Decline and Run. The caller holds [password] ([rememberTransientSecret]) and empties it when it sends either answer;
 * [canRun] is false until something is typed. Only a tap on Run sends it, never the keyboard. The password is emptied
 * when the field leaves the screen while the card stays (a folded card), and control characters never get in: the hub
 * refuses a password holding one (a pasted line break, invisible behind the mask) and runs nothing.
 */
@Composable
fun SecretRunActions(
    password: String, onPasswordChange: (String) -> Unit, editable: Boolean, canDecline: Boolean, canRun: Boolean,
    onDecline: () -> Unit, onRun: () -> Unit, modifier: Modifier = Modifier,
) {
    val clear by rememberUpdatedState(onPasswordChange)
    DisposableEffect(Unit) { onDispose { clear("") } }
    Column(modifier, verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
        JSecretField(password, { typed -> onPasswordChange(typed.filterNot(Char::isISOControl)) },
            label = stringResource(R.string.chat_request_secret_run_password), modifier = Modifier.fillMaxWidth(), enabled = editable)
        Text(stringResource(R.string.chat_request_secret_run_note), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2)
        DecisionButtons(
            primary = JAction(stringResource(R.string.chat_request_secret_run_run), enabled = canRun, onClick = onRun),
            secondary = JAction(stringResource(R.string.chat_request_secret_run_decline), enabled = canDecline, onClick = onDecline),
        )
    }
}
