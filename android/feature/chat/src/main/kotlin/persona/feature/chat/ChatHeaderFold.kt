package persona.feature.chat

import androidx.compose.animation.core.FiniteAnimationSpec
import androidx.compose.animation.core.animate
import androidx.compose.runtime.Stable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.input.nestedscroll.NestedScrollConnection
import androidx.compose.ui.input.nestedscroll.NestedScrollSource
import androidx.compose.ui.unit.Velocity
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch
import persona.core.designsystem.AppStyle

/**
 * The rules of an Agent chat's folding header (iOS `ChatView`'s `scrolled`, `settleChrome` and its idle task, #4339 C4),
 * as plain functions. A fold is a fraction, 0 open to 1 folded to the slim bar.
 */
internal object HeaderFoldRules {
    /** Left this long without a touch on the list or the header, the open header folds (iOS `chromeIdleSeconds`). */
    const val IDLE_MILLIS = 3_000L
    /** How often the idle time is looked at (iOS: every half second). */
    const val TICK_MILLIS = 500L
    /** The list's newest end, within this far of it: a lifted finger folds the header there (iOS: 40 pt). */
    const val NEWEST_EDGE_DP = 40

    /** Glass folds an Agent's chat header; Persona's conversations and the other styles keep theirs. */
    fun folds(style: AppStyle, topBar: Boolean, render: ChatRenderStyle): Boolean =
        style == AppStyle.Glass && topBar && render != ChatRenderStyle.Assistant

    /**
     * The list moved by [dy] pixels on screen (negative: toward the newest, the list being upside down): the header
     * follows as far, folding toward the newest and opening back into the history, within [range] pixels.
     */
    fun follow(fold: Float, dy: Float, range: Int): Float =
        if (range <= 0) fold else (fold - dy / range).coerceIn(0f, 1f)

    /** The finger lifted (and its fling ended): never half way. At the newest end it folds, else the nearer end. */
    fun settle(fold: Float, atNewest: Boolean): Float = when {
        atNewest -> 1f
        fold > 0f && fold < 1f -> if (fold > 0.5f) 1f else 0f
        else -> fold
    }

    /**
     * Whether the open header folds by itself: [idle] milliseconds without a touch, no finger on the list, nothing up
     * over the page ([busy]), and only while animations are on (the plan's rule: a header moving by itself is motion).
     */
    fun idleFolds(fold: Float, idle: Long, dragging: Boolean, busy: Boolean, reduceMotion: Boolean): Boolean =
        fold == 0f && idle >= IDLE_MILLIS && !dragging && !busy && !reduceMotion

    /** A request the page had not seen arrived: the header opens again, so its line says the agent needs you. */
    fun reopens(seen: Set<String>, now: Set<String>): Boolean = now.any { it !in seen }
}

/**
 * How far an Agent chat's header is folded (iOS `ChatChrome`), with what moves it: the list's scrolling, the finger
 * lifting, the idle time, and the taps and events that open it. [moves] false (animations off) jumps instead of
 * animating; [motion] is the spring it animates with.
 */
@Stable
internal class ChatHeaderFold(private val scope: CoroutineScope) {
    var fold by mutableFloatStateOf(0f)
        private set
    /** How far the open header folds, in pixels (its height less the slim bar's); 0 until it is measured. */
    var range = 0
    var moves = true
    var motion: FiniteAnimationSpec<Float>? = null
    /** A finger is on the list, or its fling still glides. */
    var dragging = false
        private set
    /** Milliseconds without a touch on the list or the header, counted by [tick]. */
    var idle = 0L
        private set
    private var job: Job? = null

    fun touched() { idle = 0 }

    fun scrolled(dy: Float) {
        touched()
        dragging = true
        job?.cancel()
        fold = HeaderFoldRules.follow(fold, dy, range)
    }

    /** Settles after a drag of the list's own; nothing for a sideways scroll inside it (a code block's). */
    fun released(atNewest: Boolean) {
        if (!dragging) return
        dragging = false
        moveTo(HeaderFoldRules.settle(fold, atNewest))
    }

    /** Opened on purpose: the full idle time again before it folds by itself. */
    fun open() {
        touched()
        moveTo(0f)
    }

    fun close() = moveTo(1f)

    private var known: Set<String>? = null

    /** The page's requests now: one it had not seen opens the header; the first call only learns them. */
    fun requests(ids: Set<String>) {
        val seen = known
        known = ids
        if (seen != null && HeaderFoldRules.reopens(seen, ids)) open()
    }

    /** [millis] more without a touch; folds once [HeaderFoldRules.idleFolds] says so. */
    fun tick(millis: Long, busy: Boolean, reduceMotion: Boolean) {
        idle += millis
        if (HeaderFoldRules.idleFolds(fold, idle, dragging, busy, reduceMotion)) close()
    }

    private fun moveTo(target: Float) {
        job?.cancel()
        val spec = motion
        if (!moves || spec == null) { fold = target; return }
        if (fold == target) return
        job = scope.launch { animate(fold, target, animationSpec = spec) { value, _ -> fold = value } }
    }

    /**
     * Follows the chat list's own scrolling, a drag or the glide after a flick (never the app's jumps, which do not go
     * through nested scrolling); [atNewest] says whether the list rests at its newest end once the finger has lifted.
     */
    fun connection(atNewest: () -> Boolean): NestedScrollConnection = object : NestedScrollConnection {
        override fun onPostScroll(consumed: Offset, available: Offset, source: NestedScrollSource): Offset {
            // At an end the list does not move, but the finger did: that drag settles too (iOS: the bounce).
            if (consumed.y != 0f || available.y != 0f) scrolled(consumed.y)
            return Offset.Zero
        }

        override suspend fun onPostFling(consumed: Velocity, available: Velocity): Velocity {
            released(atNewest())
            return Velocity.Zero
        }
    }
}

/** What a tap on an Agent chat header's title opens (iOS `infoTitleButton`, #4339 C4). */
internal enum class ChatTitleTap {
    /** Session Info, Glass's (iOS's). */
    SessionInfo,
    /** Why the hub is out of reach, while it is (Glass). */
    Connection,
    /** The status sheet, as Brief and Board always did. */
    Status;

    companion object {
        fun of(style: AppStyle, online: Boolean, hasSession: Boolean): ChatTitleTap = when {
            style != AppStyle.Glass -> Status
            !online -> Connection
            hasSession -> SessionInfo
            else -> Status
        }
    }
}
