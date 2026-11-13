package persona.feature.settings

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import persona.core.contracts.api.ExtensionOpStep
import persona.core.contracts.api.ExtensionOpTool
import persona.core.contracts.api.ExtensionOperation
import persona.core.designsystem.*
import persona.core.designsystem.icons.PhIcons

// An install, upgrade or removal in steps (docs/protocol/protocol.md "Extension install, upgrade and uninstall progress", #3277): every step with its
// state, a download's bytes as a bar, where it stopped and why, and what can be done now (retry, cancel, review the
// permissions it waits for). The words are OperationText; the hub's pushes reach it through CatalogExtensionModel.

/** The steps of op as a card on the extension's page. */
@Composable
internal fun OperationSteps(
    op: ExtensionOperation, busy: Boolean, onRetry: () -> Unit, onClose: () -> Unit, onReview: () -> Unit, modifier: Modifier = Modifier,
) {
    val res = LocalResources.current
    val palette = PersonaTheme.palette
    val type = PersonaTheme.type
    val (done, total) = OperationText.progress(op)
    val failed = op.failed
    JGroupItem(GroupPosition.Single, modifier) {
        Column(Modifier.fillMaxWidth().padding(horizontal = rowPadding, vertical = Tokens.Space.m).semantics { liveRegion = LiveRegionMode.Polite },
            verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
            Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                Text(OperationText.title(res, op), style = type.bodyStrong, color = palette.ink, maxLines = 1)
                if (op.state == ExtensionOperation.Running && total > 0) {
                    Text(stringResource(R.string.settings_ext_op_progress, done, total), style = type.caption, color = palette.ink3, maxLines = 1)
                }
                Text(OperationText.line(res, op), style = type.caption, maxLines = 1, overflow = TextOverflow.Ellipsis,
                    textAlign = TextAlign.End, modifier = Modifier.weight(1f),
                    color = when (op.state) {
                        ExtensionOperation.Failed -> palette.danger.text
                        ExtensionOperation.NeedsConsent -> palette.cautionText
                        else -> palette.ink3
                    })
            }
            OperationText.shown(op).forEach { StepRow(it, op.kind) }
            if (failed) OperationText.error(res, op)?.let { JInlineStatus(it, tone = InlineTone.Failed) }
            op.steps.firstOrNull { it.id == "nodes" }?.machines?.errors.orEmpty().forEach { (machine, error) ->
                JInlineStatus(stringResource(R.string.settings_ext_op_machine_error, machine, error), tone = InlineTone.Caution)
            }
            val cancel = OperationText.cancelable(op) || op.state == ExtensionOperation.NeedsConsent
            if (failed || cancel) Row(Modifier.fillMaxWidth().padding(top = Tokens.Space.xs), horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s, Alignment.End)) {
                if (failed) JButton(stringResource(R.string.settings_ext_op_dismiss), onClose, kind = JButtonKind.Quiet, size = JButtonSize.Small, enabled = !busy)
                if (cancel) JButton(stringResource(R.string.settings_ext_op_cancel), onClose, kind = JButtonKind.Quiet, size = JButtonSize.Small, enabled = !busy)
                if (op.state == ExtensionOperation.NeedsConsent) JButton(stringResource(R.string.settings_ext_op_review), onReview,
                    size = JButtonSize.Small, icon = PhIcons.ShieldCheck, enabled = !busy)
                if (failed && op.retryable) JButton(stringResource(R.string.settings_ext_op_retry), onRetry, size = JButtonSize.Small,
                    icon = PhIcons.ArrowClockwise, loading = busy, enabled = !busy)
            }
        }
    }
}

@Composable
private fun StepRow(s: ExtensionOpStep, kind: String) {
    val res = LocalResources.current
    val palette = PersonaTheme.palette
    val type = PersonaTheme.type
    val detail = OperationText.detail(res, s)
    val fraction = OperationText.fraction(s)
    Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
            Box(Modifier.size(Tokens.Space.Standard.l), contentAlignment = Alignment.Center) { StepMark(s.state) }
            Text(OperationText.step(res, s.id, kind), style = type.callout, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f),
                color = when (s.state) { "pending" -> palette.ink3; "failed" -> palette.danger.text; else -> palette.ink })
            if (detail.isNotEmpty()) Text(detail, style = type.caption, color = palette.ink3, maxLines = 1)
        }
        // The bar under the name: a download's share, or a moving bar while its size is not known.
        if (fraction != null || (s.state == "running" && s.id == "download")) {
            JLinearProgress(fraction, Modifier.padding(start = Tokens.Space.l + Tokens.Space.s))
        }
        // The tools step: a line per program under it, the one downloading with its bar.
        s.tools.orEmpty().forEach { ToolRow(s, it) }
    }
}

@Composable
private fun ToolRow(s: ExtensionOpStep, t: ExtensionOpTool) {
    val res = LocalResources.current
    val palette = PersonaTheme.palette
    val detail = OperationText.toolDetail(res, s, t)
    val fraction = OperationText.toolFraction(s, t)
    val indent = Modifier.padding(start = Tokens.Space.l + Tokens.Space.s)
    Column(indent, verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
            Box(Modifier.size(Tokens.Space.Standard.l), contentAlignment = Alignment.Center) { StepMark(t.state) }
            Text(t.name, style = PersonaTheme.type.caption.copy(fontFamily = PersonaTheme.type.code.fontFamily), maxLines = 1,
                overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f),
                color = when (t.state) { "pending" -> palette.ink3; "failed" -> palette.danger.text; else -> palette.ink2 })
            if (detail.isNotEmpty()) Text(detail, style = PersonaTheme.type.caption, color = palette.ink3, maxLines = 1)
        }
        if (fraction != null || (t.state == "running" && !t.reused)) JLinearProgress(fraction, Modifier.padding(start = Tokens.Space.l + Tokens.Space.s))
    }
}

@Composable
private fun StepMark(state: String) {
    val palette = PersonaTheme.palette
    when (state) {
        "running" -> JSpinner(Modifier.size(Tokens.Space.Standard.m))
        "done" -> JIcon(PhIcons.Check, stringResource(R.string.settings_ext_op_step_done), size = Tokens.Space.m, tint = palette.positive)
        "failed" -> JIcon(PhIcons.X, stringResource(R.string.settings_ext_op_step_failed), size = Tokens.Space.m, tint = palette.danger.dot)
        else -> Box(Modifier.size(Tokens.Space.Standard.s).clip(CircleShape).background(palette.line))
    }
}
