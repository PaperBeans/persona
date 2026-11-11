package persona.feature.settings

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import persona.core.contracts.api.BindPrompt
import persona.core.designsystem.*
import persona.core.designsystem.icons.PhIcons

/**
 * How a connector's sign-in step or a channel's binding step is drawn (docs/extensions/connectors.md "Login supports", #4488): fields to fill
 * in, a QR code, the link a QR code encodes when its modules did not come, a code to enter or send; or one it cannot
 * draw: a kind it does not know ([Unknown], a newer hub's) or a known one without what it shows ([Incomplete]: a QR code
 * with neither, a code step with neither the code nor a page to open, a form with no fields). Those two say so with
 * "Start again", never an empty form whose "Next" can never light up.
 */
internal enum class StepView {
    Fields, QrCode, QrLink, Code, Unknown, Incomplete;

    /** The app asks the same step again by itself until it is done (never a step it cannot draw). */
    val asksAgain: Boolean get() = this == QrCode || this == QrLink || this == Code

    companion object {
        fun of(p: BindPrompt): StepView = when (p.kind.orEmpty()) {
            "" -> if (p.fields.isNotEmpty()) Fields else Incomplete
            "qr" -> when {
                p.qr?.rows.orEmpty().isNotEmpty() -> QrCode
                isWebLink(p.url) -> QrLink
                else -> Incomplete
            }
            // An extension's device code may come with the page alone (docs/extensions/extensions.md "Sign-in").
            "device_code" -> if (!p.userCode.isNullOrBlank() || isWebLink(p.url)) Code else Incomplete
            "pair_code" -> if (p.userCode.isNullOrBlank()) Incomplete else Code
            else -> Unknown
        }

        /** An http(s) address, in any case (the web's `/^https?:\/\//i`). */
        fun isWebLink(url: String?): Boolean = url != null && (url.startsWith("https://", ignoreCase = true) || url.startsWith("http://", ignoreCase = true))
    }
}

/** A QR step without its modules ([StepView.QrLink]): the link it encodes, to copy (button or long press) and open in [app]. */
@Composable
internal fun QrLinkStep(url: String, app: String) {
    val copy = rememberCopy()
    Text(stringResource(R.string.settings_src_signin_qr_link, app), Modifier.padding(horizontal = rowPadding),
        style = PersonaTheme.type.callout, color = PersonaTheme.palette.ink2)
    Row(Modifier.padding(start = rowPadding), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.m)) {
        JSelectableText(url, PersonaTheme.type.codeSmall, PersonaTheme.palette.ink, Modifier.weight(1f), maxLines = 2)
        JIconButton(PhIcons.Copy, stringResource(R.string.settings_src_signin_copy_link), { copy(url, null) })
    }
}

/**
 * A step this app cannot draw ([StepView.Unknown] or [StepView.Incomplete]): what to do, "Start again" (asks the hub for the
 * first step again) and "Cancel": a kind it does not know needs a newer app; a known one that came without what it shows is
 * the hub's to send again. A step that only carries the hub's [BindPrompt.error] (GitHub's sign-in that did not check
 * out) is not missing anything: the error above says what happened, so only the buttons stay.
 */
@Composable
internal fun CannotDrawStep(prompt: BindPrompt, busy: Boolean, onRestart: () -> Unit, onCancel: () -> Unit) {
    val view = StepView.of(prompt)
    if (view == StepView.Unknown || prompt.error.isNullOrBlank()) {
        JNotice(stringResource(if (view == StepView.Unknown) R.string.settings_src_signin_cannot_draw else R.string.settings_src_signin_incomplete),
            status = Status.NeedsYou, icon = PhIcons.Warning)
    }
    Row(horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
        JButton(stringResource(R.string.settings_src_signin_restart), onRestart, Modifier.weight(1f), icon = PhIcons.ArrowClockwise, loading = busy)
        JButton(stringResource(R.string.settings_src_cancel), onCancel, Modifier.weight(1f), kind = JButtonKind.Secondary, enabled = !busy)
    }
}
