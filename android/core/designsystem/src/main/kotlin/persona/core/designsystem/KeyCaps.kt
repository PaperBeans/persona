package persona.core.designsystem

import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.tween
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import persona.core.designsystem.icons.PhIcon

/** How a key of a key bar is down: a modifier (Ctrl, Alt, ⇧) is up, applies to the next key, is held, or is locked. */
enum class KeyCapState { Off, Once, Held, Locked }

/**
 * A key of the terminal's key bar, in the style's chip shape. [KeyCapState.Off] is the plain key. A modifier that
 * applies to the next key only ([KeyCapState.Once]) is tinted with an outline; one held down by the finger
 * ([KeyCapState.Held]) or locked by a double tap ([KeyCapState.Locked]) is filled, and a locked one also has a bar
 * under its label (the iOS key bar's three looks). Labels are one line in the terminal's monospaced face.
 *
 * With [onClick] the key is a button; a modifier key leaves it null and brings its own press handling in [modifier]
 * (a tap, a quick second tap and a hold all mean something different).
 */
@Composable
fun JKeyCap(
    label: String,
    modifier: Modifier = Modifier,
    icon: PhIcon? = null,
    state: KeyCapState = KeyCapState.Off,
    onClick: (() -> Unit)? = null,
    enabled: Boolean = true,
    contentDescription: String? = null,
) {
    val palette = PersonaTheme.palette
    val style = PersonaTheme.style
    val shape = PersonaTheme.shapes.chipShape
    val down = state == KeyCapState.Held || state == KeyCapState.Locked
    val (fill, ink) = when {
        down -> palette.action to palette.onAction
        state == KeyCapState.Once -> (if (style == AppStyle.Brief) palette.surfaceStrong else palette.accentSoft) to
            (if (style == AppStyle.Board) palette.ink else palette.accent)
        style == AppStyle.Glass -> palette.surfaceStrong to palette.ink
        else -> palette.surface to palette.ink
    }
    val frame = when {
        state == KeyCapState.Once -> Modifier.border(1.5.dp, palette.accent, shape)
        down -> Modifier
        style == AppStyle.Glass -> Modifier.border(1.dp, Brush.verticalGradient(listOf(palette.strokeTop, palette.strokeBottom)), shape)
        style == AppStyle.Board -> Modifier.border(1.dp, palette.line, shape)
        else -> Modifier
    }
    // Held keys sink a little while the finger is on them, as on iOS. The layer always stays (see stableAlpha).
    val scale by animateFloatAsState(if (state == KeyCapState.Held && !PersonaTheme.reduceMotion) 0.94f else 1f, tween(150), label = "key")
    Box(
        modifier.heightIn(min = 48.dp.row)
            .then(if (onClick != null) Modifier.clip(shape).clickable(enabled = enabled, role = Role.Button, onClick = onClick) else Modifier)
            .semantics { if (contentDescription != null) this.contentDescription = contentDescription }
            .stableAlpha(if (enabled) 1f else 0.4f),
        contentAlignment = Alignment.Center,
    ) {
        Box(
            Modifier.graphicsLayer { scaleX = scale; scaleY = scale }.heightIn(min = 36.dp.row).widthIn(min = 44.dp).clip(shape).background(fill).then(frame)
                .padding(horizontal = 10.dp.dense),
            contentAlignment = Alignment.Center,
        ) {
            if (icon != null) JIcon(icon, null, size = 18.dp, tint = ink)
            else Text(
                label, style = PersonaTheme.type.code.copy(fontWeight = FontWeight.Medium), color = ink,
                maxLines = 1, softWrap = false, overflow = TextOverflow.Clip,
            )
            if (state == KeyCapState.Locked) Box(
                Modifier.align(Alignment.BottomCenter).padding(bottom = 4.dp.dense).size(14.dp, 2.dp).clip(CircleShape).background(ink),
            )
        }
    }
}
