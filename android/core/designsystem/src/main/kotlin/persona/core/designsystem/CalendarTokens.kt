package persona.core.designsystem

import androidx.compose.ui.unit.dp

/** The to-dos' month grid (#2046) and a to-do dragged on it (#2045); iOS `Theme.CalendarGrid`. */
object CalendarTokens {
    /** A day of the month grid, at the least: its date and three lines. */
    val monthCell = 92.dp
    /** The circle behind today's date. */
    val dayMark = 22.dp
    /** The state edge on a day's to-do. */
    val edge = 2.dp
    /** A day's to-do's corners. */
    val chipRadius = 3.dp
    /** A to-do's state dot in the day's list. */
    val dot = 8.dp
    /** How far a sideways swipe on the grid goes before it turns the month. */
    val pageSwipe = 48.dp
    /** The outline of the day a dragged to-do would land on (#2045). */
    val dropRing = 2.dp
    /** The outline of a carried to-do (in the accent) and of where it would land. */
    val liftLine = 1.dp
    /** How near the top or bottom of a month taller than its space a dragged to-do scrolls it. */
    val dragEdge = 48.dp
    /** How far that scroll goes each frame. */
    val dragScroll = 6.dp
    /** Lines a day of the month shows: its to-dos, or one fewer and "+N". */
    const val monthLines = 3
    /** A done to-do, faded. */
    const val doneFade = 0.5f
    /** A day that belongs to the month before or after. */
    const val outsideFade = 0.45f
    /** A dragged to-do where it was, while it is carried (#2045). */
    const val liftedFade = 0.35f
}
