package persona.core.designsystem

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.Text
import androidx.compose.material3.minimumInteractiveComponentSize
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import persona.core.designsystem.icons.PhIcon

/**
 * One line above the composer saying what goes on now, in a neutral pill (iOS `DockPillLabel`): a small spinner while
 * it [running], else [icon], then [text] in iOS's `captionStrong` on one line that gives way at [Tokens.Size.box280].
 * As tall as a chip ([JChipTokens]), as the Assistant's entries are. The screen reader says [spoken]; with [onClick] it
 * is a button ([onClickLabel] says what a tap does), without it only words.
 */
@Composable
fun JDockPill(text: String, running: Boolean, icon: PhIcon, spoken: String, modifier: Modifier = Modifier,
    onClick: (() -> Unit)? = null, onClickLabel: String? = null) {
    val palette = PersonaTheme.palette
    Row(
        modifier.widthIn(max = Tokens.Size.box280).heightIn(min = JChipTokens.height)
            .then(if (onClick != null) Modifier.minimumInteractiveComponentSize() else Modifier)
            .clip(CircleShape).background(palette.surfaceStrong)
            .border(JChipTokens.border, palette.strokeBottom, CircleShape)
            .then(if (onClick != null) Modifier.clickable(role = Role.Button, onClickLabel = onClickLabel, onClick = onClick) else Modifier)
            .clearAndSetSemantics { contentDescription = spoken; if (onClick != null) role = Role.Button }
            .padding(horizontal = JChipTokens.horizontalPadding),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(Tokens.Space.sm),
    ) {
        if (running) JSpinner(Modifier.size(JChipTokens.iconSize), color = palette.ink2, stroke = Tokens.Size.lineMedium)
        else JIcon(icon, null, size = JChipTokens.iconSize, tint = palette.ink2)
        Text(text, style = PersonaTheme.type.caption.copy(fontWeight = FontWeight.SemiBold), color = palette.ink,
            maxLines = 1, overflow = TextOverflow.Ellipsis)
    }
}
