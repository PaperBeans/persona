package persona.core.designsystem

import androidx.compose.ui.res.stringResource
import androidx.annotation.StringRes
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.unit.dp

enum class StepState(@StringRes val labelRes: Int) {
    Pending(R.string.designsystem_step_pending), Active(R.string.designsystem_step_active),
    Complete(R.string.designsystem_step_complete), Failed(R.string.designsystem_step_failed);

    val label: String @Composable get() = stringResource(labelRes)
}

data class ProgressStep(val title: String, val state: StepState = StepState.Pending)

/** Shows discrete stages, never an estimated percentage. Decorative segments do not animate. */
@Composable
fun StepProgress(steps: List<ProgressStep>, modifier: Modifier = Modifier) {
    if (steps.isEmpty()) return
    val palette = PersonaTheme.palette
    val summary = steps.map { stringResource(R.string.designsystem_with_state, it.title, it.state.label) }
        .joinToString(stringResource(R.string.designsystem_separator))
    val current = steps.firstOrNull { it.state == StepState.Failed }
        ?: steps.firstOrNull { it.state == StepState.Active }
        ?: steps.firstOrNull { it.state == StepState.Pending }
        ?: steps.last()
    val shape = if (PersonaTheme.style == AppStyle.Board) RoundedCornerShape(1.dp) else CircleShape
    Column(modifier.clearAndSetSemantics { contentDescription = summary }, verticalArrangement = Arrangement.spacedBy(8.dp.dense)) {
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(4.dp.dense)) {
            steps.forEach { step ->
                val color = when (step.state) {
                    StepState.Pending -> palette.line
                    StepState.Active -> if (PersonaTheme.style == AppStyle.Brief) palette.ink else palette.accent
                    StepState.Complete -> palette.positive
                    StepState.Failed -> palette.danger.dot
                }
                Box(Modifier.weight(1f).height(4.dp).background(color, shape))
            }
        }
        Text(stringResource(R.string.designsystem_labeled, current.title, current.state.label), style = PersonaTheme.type.caption, color = palette.ink2)
    }
}
