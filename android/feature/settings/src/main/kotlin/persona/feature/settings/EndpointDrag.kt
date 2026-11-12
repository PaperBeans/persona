package persona.feature.settings

import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.AnimationSpec
import androidx.compose.animation.core.snap
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.gestures.awaitEachGesture
import androidx.compose.foundation.gestures.awaitFirstDown
import androidx.compose.foundation.gestures.awaitLongPressOrCancellation
import androidx.compose.foundation.gestures.drag
import androidx.compose.foundation.gestures.scrollBy
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyListItemInfo
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.SideEffect
import androidx.compose.runtime.Stable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.runtime.withFrameNanos
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.hapticfeedback.HapticFeedback
import androidx.compose.ui.hapticfeedback.HapticFeedbackType
import androidx.compose.ui.input.pointer.changedToUp
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.input.pointer.positionChange
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalHapticFeedback
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.Tokens
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch

/**
 * The finger and the list during a drag in "Connection diagnostics"' candidate list. The order itself lives in [EndpointEditorState]
 * (it is what gets stored); this keeps the pixels: where the picked-up row is drawn, its lift, scrolling while it is
 * held near an edge, and its glide into place after a drop.
 */
@Stable
internal class EndpointDragState(
    private val list: LazyListState,
    private val editor: EndpointEditorState,
    private val scope: CoroutineScope,
) {
    /** Set from the composition ([rememberEndpointDrag]): density, motion and haptics in use. */
    internal var edge = 0f
    internal var speed = 0f
    internal var liftSpec: AnimationSpec<Float> = snap()
    internal var settleSpec: AnimationSpec<Float> = snap()
    internal var haptics: HapticFeedback? = null

    /** The row under the finger. */
    var dragging: String? by mutableStateOf(null)
        private set

    /** The row gliding into its slot after a drop. */
    var settling: String? by mutableStateOf(null)
        private set

    /** Where the row was drawn when picked up, in the list's item coordinates, and how far the finger has moved since. */
    private var pickedTop = 0f
    private var travel by mutableFloatStateOf(0f)
    private var direction = 0
    private var droppedAt = 0f
    private val lift = Animatable(0f)
    private val glide = Animatable(0f)
    private var lifting: Job? = null
    private var scrolling: Job? = null

    fun lifted(url: String): Boolean = url == dragging || url == settling

    /** How far the row [url] is drawn below its slot: under the finger while held, gliding home after a drop. */
    fun translation(url: String): Float {
        val slot = item(url)?.offset ?: return 0f
        return when (url) {
            dragging -> pickedTop + travel - slot
            settling -> (droppedAt - slot) * glide.value
            else -> 0f
        }
    }

    /** 0 at rest, 1 while held: the shadow, backing and slight scale of a picked-up row. */
    fun liftOf(url: String): Float = if (lifted(url)) lift.value else 0f

    /** Picks [url] up; [longPress] when a long press did it (the handle picks up at the first touch). */
    fun start(url: String, longPress: Boolean = false) {
        if (dragging != null) return
        val item = item(url) ?: return
        val drawn = item.offset + translation(url)
        editor.startDrag(url)
        if (editor.ui.value.drag?.url != url) return
        settling = null
        dragging = url
        pickedTop = drawn
        travel = 0f
        direction = 0
        haptics?.performHapticFeedback(if (longPress) HapticFeedbackType.LongPress else HapticFeedbackType.GestureThresholdActivate)
        lifting?.cancel()
        lifting = scope.launch { lift.animateTo(1f, liftSpec) }
        scrolling?.cancel()
        scrolling = scope.launch { scrollNearEdges() }
    }

    fun moveBy(url: String, dy: Float) {
        if (url != dragging || dy == 0f) return
        travel += dy
        direction = if (dy > 0f) 1 else -1
        reposition()
    }

    /** The finger lifted ([dropped]) or the gesture was taken away (the row goes back where it was). */
    fun stop(url: String, dropped: Boolean) {
        if (url != dragging) return
        droppedAt = pickedTop + travel
        dragging = null
        settling = url
        scrolling?.cancel()
        // Undispatched: the editor swaps the drag's order for the pending one before the next frame; storing runs on.
        if (dropped) scope.launch(start = CoroutineStart.UNDISPATCHED) { editor.endDrag() } else editor.cancelDrag()
        haptics?.performHapticFeedback(HapticFeedbackType.GestureEnd)
        lifting?.cancel()
        lifting = scope.launch {
            glide.snapTo(1f)
            launch { lift.animateTo(0f, settleSpec) }
            glide.animateTo(0f, settleSpec)
            if (settling == url) settling = null
        }
    }

    private fun item(url: String): LazyListItemInfo? = list.layoutInfo.visibleItemsInfo.firstOrNull { it.key == url }

    private fun rows(order: List<String>): List<LazyListItemInfo> {
        val keys = order.toHashSet()
        return list.layoutInfo.visibleItemsInfo.filter { it.key in keys }
    }

    /** Moves the held row past the rows its middle has crossed ([dragMove]). */
    private fun reposition() {
        val url = dragging ?: return
        val order = editor.ui.value.drag?.urls ?: return
        val visible = rows(order)
        val to = dragMove(order, visible.map { RowSpan(it.key as String, it.offset, it.size) }, url, pickedTop + travel) ?: return
        // Moving the first item on screen would make the list scroll along with it: keep the position by index.
        val first = list.firstVisibleItemIndex
        if (visible.any { it.index == first && (it.key == url || it.key == order[to]) }) {
            list.requestScrollToItem(first, list.firstVisibleItemScrollOffset)
        }
        editor.dragTo(to)
        haptics?.performHapticFeedback(HapticFeedbackType.SegmentTick)
    }

    /** While the row is held near the top or bottom of the clear part of the list, scroll towards the rows out of sight. */
    private suspend fun scrollNearEdges() {
        var last = withFrameNanos { it }
        while (true) {
            val now = withFrameNanos { it }
            val seconds = ((now - last) / 1_000_000_000f).coerceAtMost(MaxFrameSeconds)
            last = now
            val url = dragging ?: return
            val order = editor.ui.value.drag?.urls ?: return
            val info = list.layoutInfo
            val self = item(url) ?: continue
            val visible = rows(order)
            val top = pickedTop + travel
            val velocity = edgeScrollVelocity(
                top = top, bottom = top + self.size,
                // Item coordinates start below the top padding (the floating bar in glass); the bottom padding is the dock.
                start = 0f, end = (info.viewportEndOffset - info.afterContentPadding).toFloat(),
                zone = edge, maxSpeed = speed, direction = direction,
                groupTop = visible.firstOrNull { it.key == order.first() }?.offset?.toFloat(),
                groupBottom = visible.firstOrNull { it.key == order.last() }?.let { (it.offset + it.size).toFloat() },
            )
            if (velocity != 0f && list.scrollBy(velocity * seconds) != 0f) reposition()
        }
    }
}

@Composable
internal fun rememberEndpointDrag(list: LazyListState, editor: EndpointEditorState): EndpointDragState {
    val scope = rememberCoroutineScope()
    val state = remember(list, editor) { EndpointDragState(list, editor, scope) }
    val density = LocalDensity.current
    val haptics = LocalHapticFeedback.current
    val motion = PersonaTheme.motion
    val reduce = PersonaTheme.reduceMotion
    SideEffect {
        state.edge = with(density) { EdgeZone.toPx() }
        state.speed = with(density) { EdgeSpeed.toPx() }
        state.haptics = haptics
        state.liftSpec = if (reduce) snap() else tween(motion.quick)
        state.settleSpec = if (reduce) snap() else motion.enter()
    }
    return state
}

/** The handle: the row follows the finger from the first touch, no long press needed, and the list does not scroll. */
internal fun Modifier.dragHandle(state: EndpointDragState, url: String): Modifier = pointerInput(state, url) {
    awaitEachGesture {
        // A down taken before it gets here (a right click opening the row's actions) is not a drag.
        val down = awaitFirstDown(requireUnconsumed = true)
        // Taken here: the row neither opens its actions nor waits for a long press.
        down.consume()
        state.start(url)
        var dropped = false
        try {
            dropped = drag(down.id) { change ->
                state.moveBy(url, change.positionChange().y)
                change.consume()
            }
        } finally {
            state.stop(url, dropped)
        }
    }
}

/**
 * A long press anywhere on the row picks it up. Goes after the row's click in the modifier chain, so it sees the
 * down first; downs the handle or the "Test" button took are theirs.
 */
internal fun Modifier.dragAfterLongPress(state: EndpointDragState, url: String): Modifier = pointerInput(state, url) {
    awaitEachGesture {
        val down = awaitFirstDown(requireUnconsumed = true)
        val held = awaitLongPressOrCancellation(down.id) ?: return@awaitEachGesture
        state.start(url, longPress = true)
        var dropped = false
        try {
            dropped = drag(held.id) { change ->
                state.moveBy(url, change.positionChange().y)
                change.consume()
            }
            // Lifting the finger ends the drag; it must not also open the row's actions.
            if (dropped) currentEvent.changes.forEach { if (it.changedToUp()) it.consume() }
        } finally {
            state.stop(url, dropped)
        }
    }
}

/** The handle's mark: six dots in two columns (the icon set has no grip). */
@Composable
internal fun Grip(color: Color, modifier: Modifier = Modifier) {
    Canvas(modifier.size(GripWidth, GripHeight)) {
        val radius = GripDot.toPx() / 2
        val gap = GripGap.toPx()
        val x = size.width / 2
        val y = size.height / 2
        for (column in listOf(x - gap / 2, x + gap / 2)) {
            for (row in listOf(y - gap, y, y + gap)) drawCircle(color, radius, Offset(column, row))
        }
    }
}

private val GripDot = Tokens.Space.Standard.xs
private val GripGap = Tokens.Space.Standard.xs + Tokens.Space.Standard.xxs
internal val GripWidth = Tokens.Space.Standard.m
private val GripHeight = Tokens.Space.Standard.l

/** The band along each edge of the list where a held row scrolls it: one touch target. */
private val EdgeZone = Tokens.MinTouch.android

/** Scrolling speed per second at the very edge: the band sixteen times over. */
private val EdgeSpeed = Tokens.MinTouch.android * 16

/** A frame that took longer (the app was busy) scrolls as if it took this long, so the list does not leap. */
private const val MaxFrameSeconds = 0.05f
