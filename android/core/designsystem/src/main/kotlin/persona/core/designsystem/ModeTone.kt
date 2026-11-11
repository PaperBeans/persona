package persona.core.designsystem

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import persona.core.designsystem.icons.PhIcon

// A permission mode's tone (#3957, iOS `Theme.ModeTone` from #3719, docs/design/DESIGN_SYSTEM.md): how much a
// mode lets an agent do without asking. None is an agent state's color: amber would read as Working, blue as Needs you.

/** Ask First (the quiet gray), Plan Only (teal), Auto-Edit and Auto (purple), Allow Everything (danger); Unknown: not reported yet. */
enum class ModeTone { Asks, Plans, Acts, AllowsAll, Unknown }

/** [JModeButton]'s values; Plan Only's teal is iOS `Theme.ModeTone.plans`. */
object ModeToneTokens {
    val plansLight = Color(0xFF0E7C86)
    val plansDark = Color(0xFF4FD1C5)
    /** The wash behind a tinted mode's symbol (iOS 12%). */
    const val wash = 0.12f
    /** The capsule: as tall as a chip that is not tappable, its symbol's size and side padding; the target stays 48dp. */
    val capsuleHeight = 30.dp
    val iconSize = 18.dp
    val sidePadding = 12.dp
    val target = 48.dp
}

/** The tone's colors: its symbol ([StatusColors.dot]) and its wash ([StatusColors.tint]); Ask First and Unknown the quiet ink on a chip's fill. */
fun PersonaPalette.mode(tone: ModeTone): StatusColors = when (tone) {
    ModeTone.Asks, ModeTone.Unknown -> StatusColors(ink2, ink2, surfaceStrong)
    ModeTone.Plans -> (if (dark) ModeToneTokens.plansDark else ModeToneTokens.plansLight).let { StatusColors(it, it, it.copy(alpha = ModeToneTokens.wash)) }
    ModeTone.Acts -> hue(JHue.Purple).let { it.copy(tint = it.dot.copy(alpha = ModeToneTokens.wash)) }
    ModeTone.AllowsAll -> danger.copy(tint = danger.dot.copy(alpha = ModeToneTokens.wash))
}

/**
 * A permission mode as an icon-only capsule (#3957, iOS `HeaderModeCapsule`): the mode's symbol in its [tone] on its wash,
 * a small spinner in its place while a change is on its way ([changing]). The words are the [contentDescription] (and the
 * menu it opens); Ask First is never hidden, so the control stays in one place.
 */
@Composable
fun JModeButton(
    icon: PhIcon,
    tone: ModeTone,
    contentDescription: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    changing: Boolean = false,
    enabled: Boolean = true,
) {
    val colors = PersonaTheme.palette.mode(tone)
    Box(
        modifier.size(ModeToneTokens.target).clip(CircleShape)
            .clickable(enabled = enabled, role = Role.Button, onClick = onClick)
            .semantics { this.contentDescription = contentDescription },
        contentAlignment = Alignment.Center,
    ) {
        Box(
            Modifier.height(ModeToneTokens.capsuleHeight).clip(CircleShape).background(colors.tint).padding(horizontal = ModeToneTokens.sidePadding),
            contentAlignment = Alignment.Center,
        ) {
            if (changing) JSpinner(Modifier.size(ModeToneTokens.iconSize))
            else JIcon(icon, null, size = ModeToneTokens.iconSize, tint = colors.dot)
        }
    }
}
