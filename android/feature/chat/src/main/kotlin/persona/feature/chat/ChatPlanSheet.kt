package persona.feature.chat

import android.content.res.Resources
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.res.stringResource
import persona.core.designsystem.JLinearProgress
import persona.core.designsystem.JSheet
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.Tokens

/** Which step of how many a plan is on, "Step 3 of 4", or "All 4 steps done" (iOS `PlanProgress.text`). */
internal fun planStepText(res: Resources, todos: List<TodoEntry>): String {
    val progress = planProgress(todos)
    return if (progress.allDone) res.getQuantityString(R.plurals.chat_plan_sheet_all_done, progress.total, progress.total)
        else res.getString(R.string.chat_plan_sheet_step, progress.step, progress.total)
}

/**
 * The plan sheet's first line (iOS `PlanProgress.summary`): how far along, then the step it is on, "Step 3 of 4 · Make both
 * sizes' layouts"; once every step is done, that alone.
 */
internal fun planSummary(res: Resources, todos: List<TodoEntry>): String {
    val step = planStepText(res, todos)
    if (planProgress(todos).allDone) return step
    val current = (todos.firstOrNull { it.status == "in_progress" } ?: todos.firstOrNull { it.status != "completed" })?.content
    return current?.let { res.getString(R.string.chat_plan_sheet_summary, step, it) } ?: step
}

/**
 * The chat's newest plan whole (iOS `PlanSheet`), opened from the plan pill on Glass: how far along and the step it is on,
 * a bar of the steps done, then every step with its state. It follows the plan as the agent updates it.
 */
@Composable
internal fun PlanSheet(plan: LinePlan, onDismiss: () -> Unit) {
    val res = LocalResources.current
    val progress = planProgress(plan.todos)
    JSheet(onDismiss, title = stringResource(R.string.chat_plan_sheet_title), scrollable = true) {
        Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.m)) {
            Text(planSummary(res, plan.todos), style = PersonaTheme.type.callout, color = PersonaTheme.palette.ink2)
            JLinearProgress(if (progress.total > 0) progress.done.toFloat() / progress.total else 0f, color = PersonaTheme.palette.positive)
            TodoList(plan.todos, plan.failed)
        }
    }
}
