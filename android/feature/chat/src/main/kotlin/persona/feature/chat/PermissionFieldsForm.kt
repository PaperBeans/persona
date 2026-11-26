package persona.feature.chat

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.booleanOrNull
import persona.core.contracts.Interaction
import persona.core.contracts.InteractionField
import persona.core.designsystem.DecisionButtons
import persona.core.designsystem.JAction
import persona.core.designsystem.JSecretField
import persona.core.designsystem.JTextButton
import persona.core.designsystem.JTextField
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.Tokens

internal const val CommandInputTool = "command_input"

internal data class CommandInput(val command: String, val output: String, val prompt: String, val masked: Boolean)

val Interaction.isCommandInputPermission: Boolean
    get() = kind == "permission" && toolName == CommandInputTool

internal val Interaction.commandInput: CommandInput?
    get() {
        if (!isCommandInputPermission) return null
        val payload = toolInput as? JsonObject ?: return null
        fun string(key: String) = (payload[key] as? JsonPrimitive)?.takeIf { it.isString }?.content.orEmpty()
        return CommandInput(
            command = string("command"), output = string("output"),
            prompt = string("prompt").ifBlank { fields.orEmpty().firstOrNull { it.key == "input" }?.label.orEmpty() },
            masked = payload["masked"]?.let { (it as? JsonPrimitive)?.booleanOrNull ?: true } ?: false,
        )
    }

@Composable
internal fun CommandInputSummary(input: CommandInput) {
    val code = PersonaTheme.type.callout.copy(fontFamily = FontFamily.Monospace, color = PersonaTheme.palette.ink2)
    if (input.command.isNotBlank()) Text(input.command, style = code)
    if (input.output.isNotBlank()) ChatLinkText(input.output, style = code)
}

@Composable
internal fun CommandInputField(input: CommandInput, value: String, update: (String) -> Unit, editable: Boolean) {
    var masked by remember(input.masked) { mutableStateOf(input.masked) }
    var focused by remember { mutableStateOf(false) }
    val restoreFocus = remember(masked) { focused }
    // The legacy Compose editor keeps its first password semantics after a visual transformation changes.
    // Replace the editor at that boundary. Its line lives in the card and stays available to the new editor.
    key(masked) {
        val focus = remember { FocusRequester() }
        JSecretField(value, update,
            label = input.prompt.ifBlank { stringResource(R.string.chat_command_input_line) },
            modifier = Modifier.fillMaxWidth().testTag("interaction.field.input").focusRequester(focus)
                .onFocusChanged { focused = it.hasFocus },
            enabled = editable, masked = masked,
            trailing = {
                JTextButton(
                    stringResource(if (masked) R.string.chat_command_input_show else R.string.chat_command_input_hide),
                    onClick = { masked = !masked }, enabled = editable,
                    modifier = Modifier.testTag("interaction.commandInput.visibility"),
                )
            })
        if (restoreFocus) LaunchedEffect(Unit) { focus.requestFocus() }
    }
}

/**
 * A permission card's fields (#4641, iOS `PermissionInputForm`): one field per declared field, a secret one masked, then
 * Deny and Fill In & Allow (no Always); Allow stays off until every required field is filled ([canAllow]). The caller
 * holds [values] ([rememberTransient]) and empties them when it sends either answer; they are emptied too when the form
 * leaves the screen while the card stays. Control characters never get into a secret field (one pasted line break would
 * hide behind the mask).
 */
@Composable
internal fun PermissionFieldsActions(
    fields: List<InteractionField>, values: Map<String, String>, onValuesChange: (Map<String, String>) -> Unit, editable: Boolean,
    canDeny: Boolean, canAllow: Boolean, onDeny: () -> Unit, onAllow: () -> Unit, modifier: Modifier = Modifier,
) {
    val clear by rememberUpdatedState(onValuesChange)
    DisposableEffect(Unit) { onDispose { clear(emptyMap()) } }
    Column(modifier, verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
        fields.forEach { field ->
            val value = values[field.key].orEmpty()
            val tagged = Modifier.fillMaxWidth().testTag("interaction.field.${field.key}")
            if (field.secret) JSecretField(value, { typed -> onValuesChange(values + (field.key to typed.filterNot(Char::isISOControl))) },
                label = field.label, modifier = tagged, enabled = editable)
            else JTextField(value, { typed -> onValuesChange(values + (field.key to typed)) }, tagged,
                placeholder = field.placeholder ?: field.label, label = field.label, singleLine = true, enabled = editable)
        }
        DecisionButtons(
            primary = JAction(stringResource(R.string.chat_request_fill_in_allow), enabled = canAllow, onClick = onAllow),
            secondary = JAction(stringResource(R.string.chat_request_deny), enabled = canDeny, onClick = onDeny),
        )
    }
}
