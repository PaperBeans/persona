package persona.core.designsystem

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.Text
import androidx.compose.material3.minimumInteractiveComponentSize
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.text.font.FontWeight
import persona.core.designsystem.icons.PersonaIcons

/**
 * "⟳ 13:24 │ ⚙ 2" at the end of an Agent chat header's second row (iOS `ActivityChips`, #4339 C8): what keeps the agent
 * going, in one neutral pill as tall as the tabs beside it. [loop] is the loop's short words (its next time, Running,
 * Loop) after its arrows in the loop color; [background] the number of background tasks by a neutral gear (whether they
 * work or only wait is the state, which the title's line says). With both at the largest text sizes the loop is its
 * arrows alone. The screen reader says [spoken]; a tap opens their sheet ([onClick]).
 */
@Composable
fun JActivityPill(loop: String?, background: Int, spoken: String, onClick: () -> Unit, modifier: Modifier = Modifier) {
    val palette = PersonaTheme.palette
    val words = PersonaTheme.type.labelSmall.copy(fontWeight = FontWeight.SemiBold)
    val roomy = LocalDensity.current.fontScale < LargestText || background == 0
    Row(
        modifier.heightIn(min = JChipTokens.height).minimumInteractiveComponentSize().clip(CircleShape).background(palette.surfaceStrong)
            .border(JChipTokens.border, palette.strokeBottom, CircleShape)
            .clickable(role = Role.Button, onClick = onClick).clearAndSetSemantics { contentDescription = spoken; role = Role.Button }
            .padding(horizontal = Tokens.Space.m),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s),
    ) {
        if (loop != null) Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.xxs)) {
            JIcon(PersonaIcons.inProgress, null, size = JChipTokens.iconSize, tint = if (palette.dark) Tokens.Common.Dark.loop else Tokens.Common.Light.loop)
            if (roomy) Text(loop, style = words, color = palette.ink, maxLines = 1, softWrap = false)
        }
        if (loop != null && background > 0) Box(Modifier.width(Tokens.Size.line).height(Tokens.Size.markXS).background(palette.line))
        if (background > 0) Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.xxs)) {
            JIcon(PersonaIcons.backgroundTask, null, size = JChipTokens.iconSize, tint = palette.ink2)
            Text("$background", style = PersonaTheme.type.micro.copy(fontWeight = FontWeight.SemiBold), color = palette.ink, maxLines = 1, softWrap = false)
        }
    }
}

/** From this font scale on, a pill holding both keeps only the loop's arrows (iOS: Dynamic Type xxxLarge). */
private const val LargestText = 1.6f
