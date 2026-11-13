package persona.feature.chat

import android.content.res.Resources
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch
import persona.core.contracts.DelegatedAnswer
import persona.core.contracts.SessionKey
import persona.core.contracts.api.InteractionCenter
import persona.core.contracts.api.hubCodedFailure
import persona.core.designsystem.*
import persona.core.designsystem.icons.PersonaIcons
import persona.core.domain.answer.AnswerBuilder
import persona.core.domain.answer.Delegated
import persona.core.domain.answer.ReanswerForm

/**
 * The latest question the assistant answered for this Agent, over its chat's composer until put away (iOS `DelegatedCard`,
 * web `Delegated.tsx`; #4339 C9): what it answered and why, and Change answer while the chat takes messages. Glass only;
 * Session info lists them all. Draws nothing when there is none.
 */
@Composable
internal fun DelegatedCard(key: SessionKey, interactions: InteractionCenter, canChange: Boolean, modifier: Modifier = Modifier) {
    val kept by interactions.delegated.collectAsStateWithLifecycle()
    val answer = kept.lastOrNull { it.interaction.sessionKey == key } ?: return
    val changes by interactions.reanswered.collectAsStateWithLifecycle()
    val palette = PersonaTheme.palette
    var changing by remember(answer.id) { mutableStateOf(false) }
    Column(modifier.fillMaxWidth().floatingSurface(RoundedCornerShape(Tokens.Radius.panel))
        .padding(start = Tokens.Space.l, end = Tokens.Space.xs, bottom = Tokens.Space.m)) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.sm)) {
            JIcon(PersonaIcons.ai, null, size = Tokens.Size.markS, tint = palette.ai, decorative = true)
            Text(stringResource(R.string.chat_delegated_title), Modifier.weight(1f), style = PersonaTheme.type.micro.copy(fontWeight = FontWeight.SemiBold),
                color = palette.ai, maxLines = 1, overflow = TextOverflow.Ellipsis)
            JIconButton(PersonaIcons.close, stringResource(R.string.chat_delegated_close), { interactions.dropDelegated(answer.id) },
                raised = false, tint = palette.ink2)
        }
        DelegatedBody(answer, changes[answer.id], Modifier.padding(end = Tokens.Space.m), onChange = if (canChange) ({ changing = true }) else null)
    }
    if (changing) ReanswerSheet(answer, Delegated.latest(answer, changes[answer.id]), onDismiss = { changing = false }) { answers ->
        interactions.reanswer(answer.id, answers, answer.interaction.manager)
    }
}

/**
 * The assistant's answer to each question as it is now ([changed] over the assistant's), what it first answered once the
 * user changed it, why, "You changed it", and Change answer when [onChange] is given (iOS `DelegatedBody`).
 */
@Composable
internal fun DelegatedBody(answer: DelegatedAnswer, changed: Map<String, String>?, modifier: Modifier = Modifier, onChange: (() -> Unit)? = null) {
    val palette = PersonaTheme.palette
    val type = PersonaTheme.type
    val now = Delegated.latest(answer, changed)
    Column(modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
        answer.interaction.questions.orEmpty().forEach { question ->
            val said = now[question.question].orEmpty().trim()
            val first = answer.answers[question.question].orEmpty().trim()
            Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.xxs)) {
                Text(question.question, style = type.caption, color = palette.ink2)
                Text(said.ifEmpty { stringResource(R.string.chat_info_delegated_no_answer) },
                    style = type.callout.copy(fontWeight = FontWeight.SemiBold), color = palette.ink)
                if (changed != null && first.isNotEmpty() && !Delegated.sameAnswer(question, said, first)) {
                    Text(stringResource(R.string.chat_delegated_was, first), style = type.caption, color = palette.ink2)
                }
            }
        }
        if (answer.reason.isNotBlank()) Text(stringResource(R.string.chat_info_delegated_why, answer.reason.trim()),
            style = type.caption, color = palette.ink2)
        if (changed != null || onChange != null) Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
            if (changed != null) {
                JIcon(PersonaIcons.done, null, size = Tokens.Size.markS, tint = palette.positive, decorative = true)
                Text(stringResource(R.string.chat_delegated_changed), style = type.caption.copy(fontWeight = FontWeight.SemiBold),
                    color = palette.positive, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
            Spacer(Modifier.weight(1f))
            onChange?.let { JButton(stringResource(R.string.chat_delegated_change), it, kind = JButtonKind.Secondary, size = JButtonSize.Small, icon = PersonaIcons.edit) }
        }
    }
}

/**
 * Change answer (iOS `ReanswerSheet`): each question with the answers so far picked, or typed out in Other answer, and only
 * what the user changes goes to the Agent, as a message ([send], POST …/reanswer). Send waits for a change; a refusal
 * stays in the sheet with the hub's reason, and a sent one closes it with "Your new answer was sent to the agent".
 */
@Composable
internal fun ReanswerSheet(answer: DelegatedAnswer, before: Map<String, String>, onDismiss: () -> Unit, send: suspend (Map<String, String>) -> Unit) {
    val res = LocalResources.current
    val feedback = LocalAppFeedback.current
    val scope = rememberCoroutineScope()
    val palette = PersonaTheme.palette
    val questions = answer.interaction.questions.orEmpty()
    var form by remember(answer.id) { mutableStateOf(ReanswerForm.start(questions, before)) }
    var sending by remember(answer.id) { mutableStateOf(false) }
    var error by remember(answer.id) { mutableStateOf<String?>(null) }
    val changes = form.changes(questions, before)
    JSheet(onDismiss, title = stringResource(R.string.chat_delegated_change), scrollable = true, dismissible = !sending) {
        questions.forEachIndexed { i, question ->
            JSectionHeader(question.question)
            question.options.forEachIndexed { j, option ->
                val on = option.label in form.picked[i].orEmpty()
                JGroupItem(GroupPosition.of(j, question.options.size)) {
                    JRow(Modifier.semantics { selected = on }, onClick = { form = form.toggle(i, question, option.label) },
                        role = if (question.multiSelect) Role.Checkbox else Role.RadioButton, chevron = false,
                        trailing = if (on) ({ JIcon(PersonaIcons.done, null, tint = palette.accent, decorative = true) }) else null) {
                        Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.xxs)) {
                            Text(AnswerBuilder.display(option.label).text, style = PersonaTheme.type.body, color = palette.ink)
                            option.description?.takeIf(String::isNotBlank)?.let {
                                Text(it, style = PersonaTheme.type.caption, color = palette.ink2)
                            }
                        }
                    }
                }
            }
            JTextField(form.custom[i].orEmpty(), { form = form.typed(i, it) }, Modifier.fillMaxWidth().padding(top = Tokens.Space.s),
                placeholder = stringResource(R.string.chat_reanswer_other), maxLines = 4, enabled = !sending)
        }
        Text(stringResource(R.string.chat_reanswer_note), Modifier.padding(top = Tokens.Space.s), style = PersonaTheme.type.caption, color = palette.ink2)
        error?.let { JNotice(it, Modifier.padding(top = Tokens.Space.m), status = Status.NeedsYou, icon = PersonaIcons.error) }
        Row(Modifier.fillMaxWidth().padding(top = Tokens.Space.l), horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
            JButton(stringResource(R.string.chat_reanswer_cancel), onDismiss, Modifier.weight(1f), kind = JButtonKind.Secondary, enabled = !sending)
            JButton(stringResource(R.string.chat_reanswer_send), {
                val sent = changes
                if (sent.isEmpty() || sending) return@JButton
                sending = true
                error = null
                scope.launch {
                    try {
                        send(sent)
                        feedback?.notice(res.getString(R.string.chat_reanswer_sent), FeedbackStyle.Success)
                        onDismiss()
                    } catch (e: CancellationException) { throw e }
                    catch (e: Exception) { error = reanswerError(res, e) }
                    finally { sending = false }
                }
            }, Modifier.weight(1f), enabled = changes.isNotEmpty(), loading = sending)
        }
    }
}

/**
 * Why a change of answer did not go, by the hub's status (docs/protocol/protocol.md POST …/reanswer, iOS
 * `Delegated.reanswerError`); a hub without the route answers a bare 404.
 */
internal fun reanswerError(res: Resources, error: Throwable): String {
    val failure = hubCodedFailure(error)
    when (failure?.status) {
        403 -> return res.getString(R.string.chat_reanswer_answer_only)
        409 -> return res.getString(R.string.chat_reanswer_not_delegated)
        400 -> return res.getString(R.string.chat_reanswer_empty)
        502 -> return res.getString(R.string.chat_reanswer_not_sent, hubReason(error) ?: operationFeedback(error).text.resolve(res))
        503 -> if (failure.code == "assistant_offline") return res.getString(R.string.chat_reanswer_offline)
        404 -> if (failure.code.isNullOrEmpty()) return res.getString(R.string.chat_reanswer_old_hub)
    }
    if (error is UnsupportedOperationException) return res.getString(R.string.chat_reanswer_unreachable)
    return res.getString(R.string.chat_reanswer_failed, hubReason(error) ?: operationFeedback(error).text.resolve(res))
}
