package persona.core.designsystem

import androidx.compose.ui.unit.dp

/** Native capsule geometry, shared by its window and Compose content. */
object CapsuleTokens {
    const val revealScale = .94f
    const val collapseScale = .8f
    const val revealAlpha = .4f
    const val completionMillis = 4_000L
    val completionWidth = 216.dp
    val orb = 36.dp
    val compactWidth = 56.dp
    val compactRadius = 18.dp
    val face = 26.dp
    val badge = 8.dp
    /** The collapsed capsule's unread dot sits on its top-end corner, clear of the face (V7 design). */
    val badgeInset = 5.dp
    val divider = 1.dp
    val sendButton = 34.dp
    val cardWidth = 340.dp
    val cardMaxHeight = 420.dp
    val edge = 12.dp
    val padding = 12.dp.dense
    val gap = 8.dp.dense
    val smallGap = 4.dp
    val border = 1.dp
    val elevation = 6.dp
    val messageHeight = 148.dp
    val inputMaxHeight = 80.dp
    val zero = 0.dp
    val headerHeight = 51.dp
    val composerHeight = 61.dp
    val control = 32.dp
    val icon = 18.dp
    val menuMaxHeight = 260.dp
    val followBottomTolerance = 24.dp
    val targetMaxWidth = 64.dp
    const val readDelayMillis = 750L
    const val bubbleWidthFraction = .9f
}
