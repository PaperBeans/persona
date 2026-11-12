package persona.core.designsystem

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Slider
import androidx.compose.material3.SliderState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import kotlin.math.roundToInt

/**
 * One value from a range ("Relevant to me" 0–100 in steps of 5). Glass: a soft well lit in the accent with a white disc;
 * brief: an ink rule with an ink disc; board: a ruled bar filled in the accent with a square handle. [step] snaps
 * the value (and makes it a list of stops for accessibility); without it the value is continuous. Dragging and
 * tapping come from Material's slider; only the look is the style's.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun JSlider(
    value: Float,
    onValueChange: (Float) -> Unit,
    modifier: Modifier = Modifier,
    valueRange: ClosedFloatingPointRange<Float> = 0f..1f,
    step: Float? = null,
    enabled: Boolean = true,
    onValueChangeFinished: (() -> Unit)? = null,
    contentDescription: String? = null,
) {
    val stops = remember(valueRange, step) { sliderSteps(valueRange, step) }
    val interaction = remember { MutableInteractionSource() }
    Slider(
        value = value.coerceIn(valueRange.start, valueRange.endInclusive),
        onValueChange = { onValueChange(snapToStep(it, valueRange, step)) },
        modifier = modifier.fillMaxWidth()
            .then(if (contentDescription != null) Modifier.semantics { this.contentDescription = contentDescription } else Modifier)
            .stableAlpha(if (enabled) 1f else 0.4f),
        enabled = enabled,
        onValueChangeFinished = onValueChangeFinished,
        interactionSource = interaction,
        steps = stops,
        valueRange = valueRange,
        thumb = { SliderThumb() },
        track = { state -> SliderTrack(state) },
    )
}

/** Material counts the stops between the two ends; a range of 0–100 in steps of 5 has 19 of them. */
internal fun sliderSteps(range: ClosedFloatingPointRange<Float>, step: Float?): Int {
    if (step == null || step <= 0f) return 0
    val count = ((range.endInclusive - range.start) / step).roundToInt()
    return (count - 1).coerceAtLeast(0)
}

/** The nearest stop, kept in range: what a drag reports even between Material's own snaps. */
internal fun snapToStep(value: Float, range: ClosedFloatingPointRange<Float>, step: Float?): Float {
    val clamped = value.coerceIn(range.start, range.endInclusive)
    if (step == null || step <= 0f) return clamped
    return (range.start + ((clamped - range.start) / step).roundToInt() * step).coerceIn(range.start, range.endInclusive)
}

@Composable
private fun SliderThumb() {
    val palette = PersonaTheme.palette
    when (PersonaTheme.style) {
        AppStyle.Glass -> Box(Modifier.size(26.dp).shadow(3.dp, CircleShape).clip(CircleShape).background(Color.White))
        AppStyle.Brief -> Box(Modifier.size(22.dp).clip(CircleShape).background(palette.action))
        AppStyle.Board -> {
            val shape = RoundedCornerShape(4.dp)
            Box(Modifier.size(width = 18.dp, height = 26.dp).clip(shape).background(palette.surfaceOpaque).border(1.5.dp, palette.ink, shape))
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun SliderTrack(state: SliderState) {
    val palette = PersonaTheme.palette
    val style = PersonaTheme.style
    val range = state.valueRange
    val span = (range.endInclusive - range.start).takeIf { it > 0f } ?: 1f
    val fraction = ((state.value - range.start) / span).coerceIn(0f, 1f)
    val (height, radius) = when (style) {
        AppStyle.Glass -> 6.dp to 3.dp
        AppStyle.Brief -> 3.dp to 1.5.dp
        AppStyle.Board -> 10.dp to 2.dp
    }
    val rest = when (style) {
        AppStyle.Glass -> palette.surfaceStrong.compositeOn(palette.surfaceOpaque)
        AppStyle.Brief -> palette.line
        AppStyle.Board -> palette.surface
    }
    val lit = if (style == AppStyle.Brief) palette.action else palette.accent
    val outline = palette.line
    Canvas(Modifier.fillMaxWidth().height(height)) {
        val corner = CornerRadius(radius.toPx())
        drawRoundRect(rest, cornerRadius = corner)
        if (fraction > 0f) drawRoundRect(lit, size = Size(size.width * fraction, size.height), cornerRadius = corner)
        if (style == AppStyle.Board) {
            val inset = 0.5.dp.dense.toPx()
            drawRoundRect(outline, topLeft = Offset(inset, inset), size = Size(size.width - inset * 2, size.height - inset * 2),
                cornerRadius = corner, style = Stroke(1.dp.toPx()))
        }
    }
}
