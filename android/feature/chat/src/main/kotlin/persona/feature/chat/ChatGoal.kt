package persona.feature.chat

import android.content.res.Resources
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import persona.core.contracts.GoalInfo
import persona.core.designsystem.*
import persona.core.designsystem.icons.PhIcons

/** A goal's status in words, as the bar and its sheet say it (iOS `GoalInfo.statusLabel`); an unknown one as sent. */
internal fun goalStatusLabel(res: Resources, goal: GoalInfo): String = when (goal.status) {
    "active" -> res.getString(R.string.chat_goal_status_active)
    "complete" -> res.getString(R.string.chat_goal_status_complete)
    "paused" -> res.getString(R.string.chat_goal_status_paused)
    "blocked" -> res.getString(R.string.chat_goal_status_blocked)
    "usage_limited" -> res.getString(R.string.chat_goal_status_usage_limited)
    "budget_limited" -> res.getString(R.string.chat_goal_status_budget_limited)
    else -> goal.status
}

/** What it has spent so far ("12.3K / 500K tokens · 42 min" in English), null when nothing is known (iOS `GoalInfo.usage`). */
internal fun goalUsage(res: Resources, goal: GoalInfo): String? = buildList {
    goal.tokensUsed?.takeIf { it > 0 }?.let { used ->
        val budget = goal.tokenBudget?.takeIf { it > 0 }
        add(if (budget != null) res.getString(R.string.chat_goal_tokens_budget, formatTokens(used, res), formatTokens(budget, res))
            else res.getString(R.string.chat_goal_tokens, formatTokens(used, res)))
    }
    goal.seconds?.takeIf { it > 0 }?.let { s ->
        add(if (s < 3600) res.getString(R.string.chat_goal_minutes, maxOf(s / 60, 1).toInt())
            else res.getString(R.string.chat_goal_hours, (s / 3600).toInt(), (s % 3600 / 60).toInt()))
    }
}.takeIf { it.isNotEmpty() }?.joinToString(" · ")

/** The status's colours: working blue while it is worked on, done green once it holds, caution when it stopped short. */
@Composable
internal fun goalColors(goal: GoalInfo): StatusColors {
    val palette = PersonaTheme.palette
    return when {
        goal.isActive -> palette.working
        goal.isComplete -> palette.tone(Tone.Positive)
        else -> palette.tone(Tone.Caution)
    }
}

/**
 * The goal over the conversation (docs/integrations/agent-goal.md, iOS `GoalBar`): its first line and status in one row under the
 * header; a tap opens [ChatGoalSheet]. Nothing when the session has no goal; a met one stays until it is replaced or
 * cleared.
 */
@Composable
internal fun ChatGoalBar(goal: GoalInfo?) {
    if (goal == null || goal.objective.isBlank()) return
    var open by rememberSaveable { mutableStateOf(false) }
    val res = LocalResources.current
    val status = goalStatusLabel(res, goal)
    val description = stringResource(R.string.chat_goal_bar_desc, status, goal.headline)
    JActivityBar(goal.headline, PhIcons.Target, goalColors(goal), onClick = { open = true },
        modifier = Modifier.padding(horizontal = Tokens.Space.l, vertical = Tokens.Space.xs).semantics { contentDescription = description },
        trailing = status, onClickLabel = stringResource(R.string.chat_goal_open))
    if (open) ChatGoalSheet(goal, onDismiss = { open = false })
}

/** The goal opened (iOS `GoalSheet`): its status and spend, the whole objective, the latest check's reason. */
@Composable
internal fun ChatGoalSheet(goal: GoalInfo, onDismiss: () -> Unit) {
    JSheet(onDismiss = onDismiss, title = stringResource(R.string.chat_goal_title), scrollable = true) { ChatGoalDetail(goal) }
}

/** [ChatGoalSheet]'s content. */
@Composable
internal fun ChatGoalDetail(goal: GoalInfo, modifier: Modifier = Modifier) {
    val res = LocalResources.current
    val palette = PersonaTheme.palette
    val type = PersonaTheme.type
    val colors = goalColors(goal)
    Column(modifier.fillMaxWidth().padding(horizontal = Tokens.Space.l).padding(bottom = Tokens.Space.xl),
        verticalArrangement = Arrangement.spacedBy(Tokens.Space.l)) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
            JIcon(PhIcons.Target, null, size = Tokens.Space.l, tint = colors.text)
            Text(goalStatusLabel(res, goal), style = type.labelSmall.copy(fontWeight = FontWeight.SemiBold), color = colors.text, maxLines = 1)
            goalUsage(res, goal)?.let { Text(it, style = type.labelSmall, color = palette.ink2, maxLines = 1) }
        }
        SelectionContainer { Text(goal.objective, style = type.body, color = palette.ink) }
        goal.reason?.takeIf(String::isNotBlank)?.let { reason ->
            Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
                Text(stringResource(if (goal.isComplete) R.string.chat_goal_reason_met else R.string.chat_goal_reason_latest),
                    style = type.caption, color = palette.ink2)
                SelectionContainer { Text(reason, style = type.callout, color = palette.ink2) }
            }
        }
        Text(stringResource(R.string.chat_goal_hint), style = type.caption, color = palette.ink3)
    }
}
