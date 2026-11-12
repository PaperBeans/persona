package persona.core.designsystem

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Immutable
import androidx.compose.runtime.compositionLocalOf
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.composed
import androidx.compose.ui.draw.drawWithContent
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.input.pointer.PointerEventPass
import androidx.compose.ui.input.pointer.PointerEventType
import androidx.compose.ui.input.pointer.isSecondaryPressed
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import persona.core.designsystem.icons.PhIcon

/**
 * What the window allows, decided once by the shell from the WindowSizeClass and the fold posture (Jetpack
 * WindowManager's FoldingFeature). Pages never measure the window themselves.
 */
@Immutable
data class WindowLayout(
    /** Expanded width: the destinations move into a rail and lists sit beside their details. */
    val expanded: Boolean = false,
    /** Half-folded with a horizontal hinge: reading above the hinge, typing below it. */
    val tabletop: Boolean = false,
    /** The horizontal hinge in window pixels while [tabletop]. */
    val hingeTop: Float = 0f,
    val hingeBottom: Float = 0f,
)

val LocalWindowLayout = staticCompositionLocalOf { WindowLayout() }

/** Longest comfortable line for lists, conversations and long text; wider panes centre their content. */
val ReadingWidth: Dp = 760.dp

/**
 * Extra side room that keeps a page's content to [ReadingWidth] in a wide pane. [pagePadding] includes it, so
 * every page that pads its content with [pagePadding] is centred and limited without knowing the window.
 */
val LocalReadingInset = compositionLocalOf { 0.dp }

/** The side room a pane of [width] needs to keep its content to [ReadingWidth]. */
fun readingInset(width: Dp): Dp = ((width - ReadingWidth) / 2).coerceAtLeast(0.dp)

/**
 * The row a detail pane shows next to its list: a light plate and an edge in the style's accent (ink on paper).
 * Harmless in one pane, where the list is hidden while a detail is open. Brief rows have no side padding (they sit
 * on the paper's text column), so there the plate reaches out past the row and the edge lies beside its icon or
 * avatar instead of over it.
 */
fun Modifier.selectedRow(selected: Boolean): Modifier = if (!selected) this else composed {
    val palette = PersonaTheme.palette
    val brief = PersonaTheme.style == AppStyle.Brief
    val edge = if (brief) palette.ink else palette.accent
    val plate = palette.statusSurface
    val outset = if (brief) 12.dp else 0.dp
    semantics { this.selected = true }.drawWithContent {
        val out = outset.toPx()
        // The plate is an opaque surface: it lies behind the row's content, the edge over it.
        drawRoundRect(plate, topLeft = Offset(-out, 0f), size = Size(size.width + 2 * out, size.height), cornerRadius = CornerRadius(12.dp.toPx()))
        drawContent()
        drawRoundRect(edge, topLeft = Offset(-out, size.height * 0.2f), size = Size(3.dp.toPx(), size.height * 0.6f), cornerRadius = CornerRadius(2.dp.toPx()))
    }
}

/** A secondary (right) click does what a long press does: mouse and trackpad users reach the same menu. */
fun Modifier.onSecondaryClick(action: (() -> Unit)?): Modifier = if (action == null) this else pointerInput(action) {
    awaitPointerEventScope {
        while (true) {
            val event = awaitPointerEvent(PointerEventPass.Initial)
            if (event.type == PointerEventType.Press && event.buttons.isSecondaryPressed) {
                event.changes.forEach { it.consume() }
                action()
            }
        }
    }
}

/** The detail pane before anything is picked from the list beside it. */
@Composable
fun DetailPlaceholder(title: String, text: String, icon: PhIcon, modifier: Modifier = Modifier) {
    val palette = PersonaTheme.palette
    Box(modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        PageBackdrop()
        Column(Modifier.widthIn(max = 320.dp).padding(24.dp.dense), horizontalAlignment = Alignment.CenterHorizontally) {
            Box(Modifier.size(64.dp).background(palette.surfaceStrong, RoundedCornerShape(20.dp.corner)), contentAlignment = Alignment.Center) {
                JIcon(icon, null, size = 30.dp, tint = palette.ink2)
            }
            Spacer(Modifier.height(16.dp.dense))
            Text(title, style = PersonaTheme.type.title, color = palette.ink, textAlign = TextAlign.Center)
            Spacer(Modifier.height(6.dp.dense))
            Text(text, style = PersonaTheme.type.callout, color = palette.ink2, textAlign = TextAlign.Center)
        }
    }
}
