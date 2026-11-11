package persona.feature.agents

import android.content.Context
import android.text.format.DateFormat
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.gestures.detectDragGesturesAfterLongPress
import androidx.compose.foundation.gestures.scrollBy
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.Stable
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.snapshotFlow
import androidx.compose.runtime.withFrameNanos
import androidx.compose.ui.layout.Layout
import androidx.compose.ui.layout.LayoutCoordinates
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.semantics.CustomAccessibilityAction
import androidx.compose.ui.semantics.customActions
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.IntSize
import androidx.compose.ui.unit.LayoutDirection
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.drop
import persona.core.contracts.TodoDue
import persona.core.designsystem.Haptic
import persona.core.designsystem.JButton
import persona.core.designsystem.JButtonKind
import persona.core.designsystem.JDatePickerDialog
import persona.core.designsystem.JRow
import persona.core.designsystem.JSwitchRow
import persona.core.designsystem.rowPadding
import persona.core.designsystem.JTimePickerDialog
import persona.core.designsystem.rememberHaptics
import persona.core.domain.tasks.DefaultDueTime
import persona.core.domain.tasks.dropOnDay
import persona.core.domain.tasks.edgeScroll
import persona.core.domain.tasks.monthDropIndex
import java.time.LocalTime
import kotlin.math.roundToInt
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectHorizontalDragGestures
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.IntrinsicSize
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.onClick
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import persona.core.contracts.Session
import persona.core.contracts.SessionKey
import persona.core.contracts.Todo
import persona.core.contracts.TodoState
import persona.core.designsystem.CalendarTokens
import persona.core.designsystem.JAction
import persona.core.designsystem.JIcon
import persona.core.designsystem.JIconButton
import persona.core.designsystem.JMenu
import persona.core.designsystem.JSegment
import persona.core.designsystem.JSegmented
import persona.core.designsystem.JSheet
import persona.core.designsystem.JTag
import persona.core.designsystem.JTextButton
import persona.core.designsystem.PersonaPalette
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.Status
import persona.core.designsystem.StatusColors
import persona.core.designsystem.Tokens
import persona.core.designsystem.icons.PhIcons
import persona.core.domain.tasks.CalendarEntry
import persona.core.domain.tasks.calendarEntries
import persona.core.domain.tasks.fitDay
import persona.core.domain.tasks.monthDays
import persona.core.domain.tasks.undatedCount
import persona.core.domain.tasks.weekStart
import persona.core.domain.tasks.weekdays
import java.time.Instant
import java.time.LocalDate
import java.time.YearMonth
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.time.format.TextStyle
import java.util.Locale
import kotlin.math.abs

// The to-dos as a month (#2046, docs/assistant/todos.md "Calendar"): the to-dos with a due date on the days of a month,
// drawn as the list draws them (state, overdue, done, priority, reminder). Tapping a day lists its to-dos; those without
// a due date are counted under the month, a tap from the list. A to-do is moved to another day by dragging it, or by
// TalkBack's "Change Due Date…" (#2045). iOS TodoCalendarView / TodoCalendarMonth / TodoCalendarDrag, web TodoCalendar.

/** The to-dos page's two views: the list, or the month of those with a due date. */
internal enum class TodosView { List, Calendar }

/** The view and "Show done" this device keeps for the to-dos page (iOS `todos.calendar.*`). */
internal class TodoCalendarPrefs(context: Context) {
    private val prefs = context.applicationContext.getSharedPreferences("todos_calendar", Context.MODE_PRIVATE)

    var view: TodosView
        get() = if (prefs.getString("view", null) == "calendar") TodosView.Calendar else TodosView.List
        set(value) = prefs.edit().putString("view", if (value == TodosView.Calendar) "calendar" else "list").apply()

    var showDone: Boolean
        get() = prefs.getBoolean("show_done", true)
        set(value) = prefs.edit().putBoolean("show_done", value).apply()
}

/** The top of the to-dos page: List | Calendar, then the list's [listControls] (#3740), or in the calendar its options ("Show done"). */
@Composable
internal fun TodosViewBar(
    view: TodosView,
    onView: (TodosView) -> Unit,
    showDone: Boolean,
    onShowDone: (Boolean) -> Unit,
    modifier: Modifier = Modifier,
    listControls: (@Composable () -> Unit)? = null,
) {
    Row(modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
        JSegmented(
            listOf(JSegment(stringResource(R.string.agents_todos_view_list)), JSegment(stringResource(R.string.agents_todos_view_calendar))),
            selected = view.ordinal,
            onSelect = { onView(TodosView.entries[it]) },
            modifier = Modifier.testTag("todos-view"),
        )
        Spacer(Modifier.weight(1f))
        if (view == TodosView.List) listControls?.invoke()
        if (view == TodosView.Calendar) {
            var open by remember { mutableStateOf(false) }
            Box {
                JIconButton(PhIcons.DotsThree, stringResource(R.string.agents_todo_calendar_options), { open = true },
                    modifier = Modifier.testTag("todo-calendar-options"))
                JMenu(open, { open = false }, listOf(
                    JAction(stringResource(R.string.agents_todo_calendar_show_done), checked = showDone) {
                        open = false
                        onShowDone(!showDone)
                    },
                ))
            }
        }
    }
}

/**
 * The month: its title with Today, previous and next (a sideways swipe turns it too), the weekdays from the one weeks
 * start on, and a day's to-dos in up to [CalendarTokens.monthLines] lines. "N without a due date" is the page's bottom
 * bar ([TodoCalendarUndatedBar]), so the glass page's soft bottom edge never covers it.
 *
 * With [onMove], a to-do drawn in a day is picked up by a long press (the system's long-press time) and dropped on
 * another day, at the same time there (#2045): it fades where it was, follows the finger with where it would land, the
 * day under it is outlined, and near the top or bottom of a month taller than its space the month scrolls. TalkBack
 * and Switch Access move one through "Change Due Date…" instead, on its day and in the day's sheet.
 */
@Composable
internal fun TodoCalendarMonth(
    todos: List<Todo>,
    sessionLookup: (SessionKey) -> Session?,
    showDone: Boolean,
    now: Instant,
    onOpenTodo: (String) -> Unit,
    /** The page's own bottom inset (its bottom bar: "N without a due date"), under the month's last line. */
    bottomPadding: Dp,
    modifier: Modifier = Modifier,
    /** A to-do dropped on another day or given a due date through "Change Due Date…" (#2045); null: none moves. */
    onMove: ((String, TodoDue) -> Unit)? = null,
) {
    val zone = ZoneId.systemDefault()
    val locale = LocalConfiguration.current.locales[0]
    val first = remember(locale) { weekStart(locale) }
    val today = now.atZone(zone).toLocalDate()
    var shown by rememberSaveable { mutableStateOf(YearMonth.from(today).toString()) }
    val month = YearMonth.parse(shown)
    var picked by rememberSaveable { mutableStateOf<String?>(null) }
    val days = remember(month, first) { monthDays(month, first) }
    val entries = remember(todos, sessionLookup, showDone, days, now) {
        calendarEntries(todos, sessionLookup, showDone, days.first(), days.last().plusDays(1), now, zone)
    }
    val step = rememberUpdatedState { n: Int -> shown = month.plusMonths(n.toLong()).toString() }
    var rescheduling by rememberSaveable { mutableStateOf<String?>(null) }
    var drag by remember { mutableStateOf<MonthDrag?>(null) }
    val viewport = remember { Placed() }
    val haptics = rememberHaptics()

    Column(modifier) {
        val title = remember(month, locale) {
            DateTimeFormatter.ofPattern(DateFormat.getBestDateTimePattern(locale, "yMMMM"), locale).format(month)
        }
        Row(
            Modifier.fillMaxWidth().padding(start = Tokens.Space.l, end = Tokens.Space.s, top = Tokens.Space.s, bottom = Tokens.Space.xs),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(Tokens.Space.xs),
        ) {
            Text(title, style = PersonaTheme.type.title, color = PersonaTheme.palette.ink, maxLines = 1, overflow = TextOverflow.Ellipsis,
                modifier = Modifier.weight(1f).testTag("todo-calendar-title"))
            JTextButton(stringResource(R.string.agents_todo_calendar_today), { shown = YearMonth.from(today).toString() })
            JIconButton(PhIcons.CaretLeft, stringResource(R.string.agents_todo_calendar_previous), { step.value(-1) })
            JIconButton(PhIcons.CaretRight, stringResource(R.string.agents_todo_calendar_next), { step.value(1) })
        }
        val names = remember(first, locale) { weekdays(first).map { it.getDisplayName(TextStyle.NARROW, locale) } }
        Row(Modifier.fillMaxWidth().padding(horizontal = Tokens.Space.xs, vertical = Tokens.Space.xs)) {
            for (name in names) {
                Text(name, style = PersonaTheme.type.micro, color = PersonaTheme.palette.ink3, maxLines = 1, textAlign = TextAlign.Center,
                    modifier = Modifier.weight(1f))
            }
        }
        val line = PersonaTheme.palette.line
        val swipe = CalendarTokens.pageSwipe
        BoxWithConstraints(
            Modifier.weight(1f).fillMaxWidth().onGloballyPositioned { viewport.coordinates = it }.pointerInput(Unit) {
                var dx = 0f
                detectHorizontalDragGestures(onDragStart = { dx = 0f }, onDragEnd = {
                    if (abs(dx) > swipe.toPx()) step.value(if (dx < 0) 1 else -1)
                }) { _, delta -> dx += delta }
            },
        ) {
            val rows = (days.size / 7).coerceAtLeast(1)
            val cell = maxOf(CalendarTokens.monthCell, maxHeight / rows)
            val scrolls = cell * rows > maxHeight
            val scroll = rememberScrollState()
            val density = LocalDensity.current
            val rtl = LocalLayoutDirection.current == LayoutDirection.Rtl
            val width = constraints.maxWidth.toFloat()
            val height = constraints.maxHeight.toFloat()
            val cellPx = with(density) { cell.toPx() }
            // The day under the finger, the month scrolled or not.
            val dayUnder = { d: MonthDrag -> monthDropIndex(d.finger.x, d.finger.y + scroll.value, width, cellPx, rows, rtl)?.let(days::getOrNull) }
            val target by remember(drag, days, width, cellPx, rows, rtl) { derivedStateOf { drag?.let(dayUnder) } }
            LaunchedEffect(drag) {
                if (drag == null) return@LaunchedEffect
                // A tick at each new day, as iOS (Haptics.selection)
                snapshotFlow { target }.drop(1).collect { if (it != null) haptics(Haptic.Selection) }
            }
            if (scrolls) LaunchedEffect(drag) {
                val d = drag ?: return@LaunchedEffect
                val edge = with(density) { CalendarTokens.dragEdge.toPx() }
                val by = with(density) { CalendarTokens.dragScroll.toPx() }
                snapshotFlow { edgeScroll(d.finger.y, height, edge) }.collectLatest { way ->
                    while (way != 0) {
                        withFrameNanos { }
                        if (scroll.scrollBy(way * by) == 0f) break
                    }
                }
            }
            val moves = onMove?.let { move ->
                EntryMoves(
                    lift = { entry, chip, at ->
                        viewport.coordinates?.takeIf { it.isAttached && chip.isAttached }?.let { grid ->
                            drag = MonthDrag(entry, at, chip.size, grid.localPositionOf(chip, at))
                            haptics(Haptic.Tap)
                        }
                    },
                    carry = { chip, at ->
                        val d = drag
                        val grid = viewport.coordinates
                        if (d != null && grid != null && grid.isAttached && chip.isAttached) d.finger = grid.localPositionOf(chip, at)
                    },
                    drop = {
                        val d = drag
                        drag = null
                        val day = d?.let(dayUnder)
                        if (d != null && day != null) move(d.entry.todoId, d.entry.dropOnDay(day, zone))
                    },
                    cancel = { drag = null },
                )
            }
            Column(Modifier.fillMaxWidth().then(if (scrolls) Modifier.verticalScroll(scroll) else Modifier)) {
                for (r in 0 until rows) {
                    Row(Modifier.fillMaxWidth().height(cell).drawBehind { drawLine(line, Offset.Zero, Offset(size.width, 0f), Stroke.HairlineWidth) }) {
                        for (c in 0 until 7) {
                            val day = days[r * 7 + c]
                            MonthDay(
                                day = day,
                                inMonth = YearMonth.from(day) == month,
                                today = day == today,
                                entries = entries[day].orEmpty(),
                                locale = locale,
                                onPick = { picked = day.toString() },
                                modifier = Modifier.weight(1f).fillMaxHeight().drawBehind {
                                    if (c > 0) drawLine(line, Offset.Zero, Offset(0f, size.height), Stroke.HairlineWidth)
                                },
                                lifted = drag?.entry?.todoId,
                                dropTarget = drag != null && target == day,
                                moves = moves,
                                onReschedule = if (onMove != null) { id -> rescheduling = id } else null,
                            )
                        }
                    }
                }
            }
            drag?.let { d -> MonthDragGhost(d, target?.let { dueWords(d.entry.dropOnDay(it, zone), locale, zone, now) }) }
        }
        Spacer(Modifier.height(bottomPadding))
    }

    picked?.let { LocalDate.parse(it) }?.let { day ->
        TodoCalendarDaySheet(day, entries[day].orEmpty(), locale, onDismiss = { picked = null }, onOpen = { id ->
            picked = null
            onOpenTodo(id)
        }, onReschedule = if (onMove != null) { id -> picked = null; rescheduling = id } else null)
    }
    rescheduling?.let { id -> todos.firstOrNull { it.id == id } }?.let { todo ->
        TodoRescheduleSheet(todo, zone, locale, onDismiss = { rescheduling = null }, onSave = { due ->
            rescheduling = null
            onMove?.invoke(todo.id, due)
        })
    }
}

/** Where a laid-out part is, for gestures to read (never drawn, so not state). */
private class Placed { var coordinates: LayoutCoordinates? = null }

/** A to-do being carried (#2045): which, where in its chip it was picked up, the chip's size, and the finger in the grid's part in sight. */
@Stable
private class MonthDrag(val entry: CalendarEntry, val grab: Offset, val size: IntSize, finger: Offset) {
    var finger by mutableStateOf(finger)
}

/** What a to-do drawn in a day does when it is picked up, carried and let go: from its chip's coordinates and a point in them. */
private class EntryMoves(
    val lift: (CalendarEntry, LayoutCoordinates, Offset) -> Unit,
    val carry: (LayoutCoordinates, Offset) -> Unit,
    val drop: () -> Unit,
    val cancel: () -> Unit,
)

/** The carried to-do under the finger, its chip lifted off the month, and above it where it would land ("Thu, Nov 19, 18:00"). */
@Composable
private fun MonthDragGhost(drag: MonthDrag, landingWords: String?) {
    val palette = PersonaTheme.palette
    val density = LocalDensity.current
    val gap = Tokens.Space.xs
    val chip = RoundedCornerShape(CalendarTokens.chipRadius)
    val label = RoundedCornerShape(Tokens.Radius.control)
    Layout(
        content = {
            Row(
                Modifier.size(with(density) { drag.size.width.toDp() }, with(density) { drag.size.height.toDp() })
                    .clip(chip).background(palette.surfaceOpaque).border(CalendarTokens.liftLine, palette.accent, chip)
                    .testTag("todo-calendar-dragging"),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Box(Modifier.width(CalendarTokens.edge).fillMaxHeight().background(palette.entryColors(drag.entry).dot))
                Text(
                    drag.entry.title,
                    style = PersonaTheme.type.micro,
                    color = if (drag.entry.overdue) palette.danger.text else palette.ink,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.padding(start = Tokens.Space.xs, end = Tokens.Space.xxs),
                )
            }
            if (landingWords != null) {
                Text(
                    landingWords,
                    style = PersonaTheme.type.caption,
                    color = palette.ink,
                    maxLines = 1,
                    modifier = Modifier.clip(label).background(palette.surfaceOpaque).border(CalendarTokens.liftLine, palette.line, label)
                        .padding(horizontal = Tokens.Space.s, vertical = Tokens.Space.xxs).testTag("todo-calendar-landing"),
                )
            }
        },
        modifier = Modifier.fillMaxSize(),
    ) { measurables, constraints ->
        val loose = constraints.copy(minWidth = 0, minHeight = 0)
        val carried = measurables[0].measure(loose)
        val landing = measurables.getOrNull(1)?.measure(loose)
        layout(constraints.maxWidth, constraints.maxHeight) {
            // read here, so a move only places them again
            val x = (drag.finger.x - drag.grab.x).roundToInt()
            val y = (drag.finger.y - drag.grab.y).roundToInt()
            carried.place(x, y)
            landing?.let {
                val left = (x + carried.width / 2 - it.width / 2).coerceIn(0, (constraints.maxWidth - it.width).coerceAtLeast(0))
                it.place(left, (y - gap.roundToPx() - it.height).coerceAtLeast(0))
            }
        }
    }
}

/**
 * A day of the month: its date (today in a circle), its to-dos, "+N" for those that do not fit; a tap lists them all.
 * For TalkBack it is one element, with "Change Due Date: <title>…" for each of its to-dos when they can move ([onReschedule]).
 */
@Composable
private fun MonthDay(
    day: LocalDate,
    inMonth: Boolean,
    today: Boolean,
    entries: List<CalendarEntry>,
    locale: Locale,
    onPick: () -> Unit,
    modifier: Modifier = Modifier,
    /** The to-do being carried, faded here while it is. */
    lifted: String? = null,
    /** It would land here: outlined in the accent. */
    dropTarget: Boolean = false,
    moves: EntryMoves? = null,
    onReschedule: ((String) -> Unit)? = null,
) {
    val palette = PersonaTheme.palette
    val res = agentResources()
    val fit = fitDay(entries, CalendarTokens.monthLines)
    val spoken = remember(day, entries, locale) {
        (listOf(DateTimeFormatter.ofPattern(DateFormat.getBestDateTimePattern(locale, "yMMMMdEEEE"), locale).format(day)) +
            entries.map { it.title }).joinToString(", ")
    }
    val actions = if (onReschedule == null) emptyList() else entries.map { e ->
        CustomAccessibilityAction(res.getString(R.string.agents_todo_calendar_change_due_of, e.title)) { onReschedule(e.todoId); true }
    }
    val ring = RoundedCornerShape(CalendarTokens.chipRadius)
    Column(
        modifier.clickable(onClick = onPick)
            .clearAndSetSemantics {
                contentDescription = spoken
                role = Role.Button
                testTag = "todo-calendar-day-$day"
                onClick { onPick(); true }
                if (actions.isNotEmpty()) customActions = actions
            }
            .then(if (dropTarget) Modifier.background(palette.accentSoft, ring).border(CalendarTokens.dropRing, palette.accent, ring) else Modifier)
            .alpha(if (inMonth) 1f else CalendarTokens.outsideFade)
            .padding(horizontal = Tokens.Space.xxs, vertical = Tokens.Space.xs),
        verticalArrangement = Arrangement.spacedBy(Tokens.Space.xxs),
    ) {
        Box(
            Modifier.align(Alignment.CenterHorizontally).size(CalendarTokens.dayMark).clip(CircleShape)
                .then(if (today) Modifier.background(palette.accent) else Modifier),
            contentAlignment = Alignment.Center,
        ) {
            Text(
                day.dayOfMonth.toString(),
                style = if (today) PersonaTheme.type.micro.copy(fontWeight = FontWeight.Bold) else PersonaTheme.type.micro,
                color = if (today) palette.onAccent else palette.ink,
                maxLines = 1,
            )
        }
        for (e in fit.shown) MonthEntry(e, lifted = e.todoId == lifted, moves = moves)
        if (fit.more > 0) {
            Text(stringResource(R.string.agents_todo_calendar_more, fit.more), style = PersonaTheme.type.micro, color = palette.ink3,
                maxLines = 1, modifier = Modifier.padding(start = Tokens.Space.xs))
        }
    }
}

/**
 * A to-do in a day of the month: its title on a thin edge in its state's color, red once overdue, faded and struck
 * through when done. With [moves], a long press picks it up and the finger carries it (#2045); moving first is the
 * month's (a swipe turns it, a drag scrolls it), and a tap is the day's.
 */
@Composable
private fun MonthEntry(entry: CalendarEntry, lifted: Boolean = false, moves: EntryMoves? = null) {
    val palette = PersonaTheme.palette
    val placed = remember { Placed() }
    val current by rememberUpdatedState(entry)
    val handler by rememberUpdatedState(moves)
    Row(
        Modifier.fillMaxWidth().height(IntrinsicSize.Min)
            .then(if (moves == null) Modifier else Modifier.onGloballyPositioned { placed.coordinates = it }.pointerInput(entry.todoId) {
                detectDragGesturesAfterLongPress(
                    onDragStart = { at -> placed.coordinates?.let { handler?.lift?.invoke(current, it, at) } },
                    onDrag = { change, _ ->
                        change.consume()
                        placed.coordinates?.let { handler?.carry?.invoke(it, change.position) }
                    },
                    onDragEnd = { handler?.drop?.invoke() },
                    onDragCancel = { handler?.cancel?.invoke() },
                )
            })
            .clip(RoundedCornerShape(CalendarTokens.chipRadius)).background(palette.sunken)
            .alpha(when {
                lifted -> CalendarTokens.liftedFade
                entry.done -> CalendarTokens.doneFade
                else -> 1f
            }),
    ) {
        Box(Modifier.width(CalendarTokens.edge).fillMaxHeight().background(palette.entryColors(entry).dot))
        Text(
            entry.title,
            style = PersonaTheme.type.micro.copy(textDecoration = if (entry.done) TextDecoration.LineThrough else null),
            color = if (entry.overdue) palette.danger.text else palette.ink,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.padding(start = Tokens.Space.xs, end = Tokens.Space.xxs, top = Tokens.Space.xxs, bottom = Tokens.Space.xxs),
        )
    }
}

/** "N without a due date · See list": back to the list, where they are; nothing when every open to-do has a due date. */
@Composable
internal fun TodoCalendarUndatedBar(todos: List<Todo>, onShowList: () -> Unit, modifier: Modifier = Modifier) {
    val count = remember(todos) { undatedCount(todos) }
    if (count == 0) return
    val palette = PersonaTheme.palette
    val line = palette.line
    Row(
        modifier.fillMaxWidth()
            .drawBehind { drawLine(line, Offset.Zero, Offset(size.width, 0f), Stroke.HairlineWidth) }
            .clickable(onClick = onShowList)
            .padding(horizontal = Tokens.Space.l, vertical = Tokens.Space.m)
            .testTag("todo-calendar-undated"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s, Alignment.CenterHorizontally),
    ) {
        JIcon(PhIcons.ListChecks, null, size = Tokens.Space.l, tint = palette.ink3)
        Text(pluralStringResource(R.plurals.agents_todo_calendar_undated, count, count), style = PersonaTheme.type.caption,
            color = palette.ink2, maxLines = 1)
        Text(stringResource(R.string.agents_todo_calendar_see_list), style = PersonaTheme.type.caption.copy(fontWeight = FontWeight.SemiBold),
            color = palette.accent, maxLines = 1)
    }
}

/**
 * A day's to-dos, all of them in the month's order: state, title, when, high priority and reminder; a tap opens one,
 * and TalkBack's "Change Due Date…" moves one ([onReschedule], #2045).
 */
@Composable
private fun TodoCalendarDaySheet(
    day: LocalDate,
    entries: List<CalendarEntry>,
    locale: Locale,
    onDismiss: () -> Unit,
    onOpen: (String) -> Unit,
    onReschedule: ((String) -> Unit)? = null,
) {
    val title = remember(day, locale) { DateTimeFormatter.ofPattern(DateFormat.getBestDateTimePattern(locale, "MMMMdEEEE"), locale).format(day) }
    JSheet(onDismiss = onDismiss, title = title, scrollable = true) {
        Column(Modifier.fillMaxWidth().padding(bottom = Tokens.Space.l).testTag("todo-calendar-day-sheet")) {
            if (entries.isEmpty()) {
                Text(stringResource(R.string.agents_todo_calendar_nothing_due), style = PersonaTheme.type.body, color = PersonaTheme.palette.ink3,
                    modifier = Modifier.padding(vertical = Tokens.Space.m))
            }
            for (e in entries) DayEntryRow(e, locale, onReschedule?.let { { it(e.todoId) } }) { onOpen(e.todoId) }
        }
    }
}

@Composable
private fun DayEntryRow(entry: CalendarEntry, locale: Locale, onReschedule: (() -> Unit)?, onOpen: () -> Unit) {
    val palette = PersonaTheme.palette
    val changeDue = stringResource(R.string.agents_todo_calendar_change_due_action)
    val time = entry.time?.let { DateTimeFormatter.ofPattern(DateFormat.getBestDateTimePattern(locale, "HHmm"), locale).format(it) }
    val whenText = when {
        time == null -> stringResource(R.string.agents_todo_calendar_all_day)
        entry.overdue -> stringResource(R.string.agents_todo_was_due, time)
        else -> time
    }
    Row(
        Modifier.fillMaxWidth().clickable(onClick = onOpen)
            .then(if (onReschedule == null) Modifier else Modifier.semantics {
                customActions = listOf(CustomAccessibilityAction(changeDue) { onReschedule(); true })
            })
            .alpha(if (entry.done) CalendarTokens.doneFade else 1f)
            .padding(vertical = Tokens.Space.m),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(Tokens.Space.m),
    ) {
        Box(Modifier.size(CalendarTokens.dot).clip(CircleShape).background(palette.entryColors(entry).dot))
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(Tokens.Space.xxs)) {
            Text(entry.title, style = PersonaTheme.type.body.copy(textDecoration = if (entry.done) TextDecoration.LineThrough else null),
                color = palette.ink, maxLines = 2, overflow = TextOverflow.Ellipsis)
            Text(whenText, style = PersonaTheme.type.caption, color = if (entry.overdue) palette.danger.text else palette.ink3, maxLines = 1)
        }
        if (entry.high) JIcon(PhIcons.Flag, stringResource(R.string.agents_todo_priority_high), size = Tokens.Space.l, tint = palette.cautionText)
        if (entry.reminding) JIcon(PhIcons.BellSimple, stringResource(R.string.agents_todo_calendar_reminder), size = Tokens.Space.l, tint = palette.ink3)
        JTag(entry.state.label)
    }
}

/**
 * A to-do's due date set without dragging it (#2045): TalkBack's and Switch Access's "Change Due Date…", as iOS
 * `TodoRescheduleSheet`. A day, All day, and the time when it is not (18:00 for an all-day one given a time); saved as a
 * drop is, with Undo.
 */
@Composable
private fun TodoRescheduleSheet(todo: Todo, zone: ZoneId, locale: Locale, onSave: (TodoDue) -> Unit, onDismiss: () -> Unit) {
    val start = todo.due
    var allDay by rememberSaveable(todo.id) { mutableStateOf(start is TodoDue.Day) }
    var day by rememberSaveable(todo.id) {
        mutableStateOf(when (start) {
            is TodoDue.At -> start.at.atZone(zone).toLocalDate()
            is TodoDue.Day -> start.day
            null -> LocalDate.now(zone)
        }.toString())
    }
    var time by rememberSaveable(todo.id) { mutableStateOf(((start as? TodoDue.At)?.at?.atZone(zone)?.toLocalTime() ?: DefaultDueTime).toString()) }
    var pickingDay by remember { mutableStateOf(false) }
    var pickingTime by remember { mutableStateOf(false) }
    val date = LocalDate.parse(day)
    val at = LocalTime.parse(time)
    val dayText = remember(date, locale) { DateTimeFormatter.ofPattern(DateFormat.getBestDateTimePattern(locale, "yMMMdEEE"), locale).format(date) }
    val timeText = remember(at, locale) { DateTimeFormatter.ofPattern(DateFormat.getBestDateTimePattern(locale, "HHmm"), locale).format(at) }
    val timeLabel = stringResource(R.string.agents_todo_calendar_time)
    JSheet(onDismiss = onDismiss, title = stringResource(R.string.agents_todo_calendar_change_due)) {
        Column(Modifier.fillMaxWidth().padding(bottom = Tokens.Space.l).testTag("todo-calendar-reschedule")) {
            Text(todo.title, style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink3, maxLines = 1, overflow = TextOverflow.Ellipsis,
                modifier = Modifier.padding(start = rowPadding, end = rowPadding, bottom = Tokens.Space.xs))
            JRow(stringResource(R.string.agents_todo_calendar_date), value = dayText, onClick = { pickingDay = true })
            JSwitchRow(stringResource(R.string.agents_todo_calendar_all_day), allDay, { allDay = it })
            if (!allDay) JRow(timeLabel, value = timeText, onClick = { pickingTime = true })
            Row(Modifier.fillMaxWidth().padding(top = Tokens.Space.m), horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                JButton(stringResource(R.string.agents_todo_cancel), onDismiss, Modifier.weight(1f), kind = JButtonKind.Secondary)
                JButton(stringResource(R.string.agents_todo_save), {
                    onSave(if (allDay) TodoDue.Day(date) else TodoDue.At(date.atTime(at).atZone(zone).toInstant()))
                }, Modifier.weight(1f).testTag("todo-calendar-reschedule-save"))
            }
        }
    }
    if (pickingDay) JDatePickerDialog(date, { day = it.toString(); pickingDay = false }, { pickingDay = false })
    if (pickingTime) JTimePickerDialog(at, { time = it.toString(); pickingTime = false }, { pickingTime = false }, title = timeLabel)
}

/** An entry's colors, as the list draws its to-do: its state's, red once overdue (what is late is what to see first). */
private fun PersonaPalette.entryColors(entry: CalendarEntry): StatusColors = when {
    entry.overdue -> danger
    entry.state == TodoState.NeedsYou -> status(Status.NeedsYou)
    entry.state == TodoState.Unread -> status(Status.Unread)
    entry.state == TodoState.Working -> status(Status.Working)
    else -> status(Status.Idle)
}
