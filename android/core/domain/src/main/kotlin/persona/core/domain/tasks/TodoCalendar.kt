package persona.core.domain.tasks

import persona.core.contracts.Session
import persona.core.contracts.SessionKey
import persona.core.contracts.Todo
import persona.core.contracts.TodoDue
import persona.core.contracts.TodoState
import persona.core.contracts.parseTime
import persona.core.contracts.state
import java.time.DayOfWeek
import java.time.Instant
import java.time.LocalDate
import java.time.LocalTime
import java.time.YearMonth
import java.time.ZoneId
import java.time.temporal.TemporalAdjusters
import java.util.Locale

// The to-dos' month (#2046, docs/assistant/todos.md "Calendar"): which to-dos a day shows and in what order, the weeks
// a month touches and the weekday they start on; where a to-do dragged onto another day lands (#2045). The rules of iOS apple/Shared/TodoCalendar.swift and the web's
// web/src/lib/todo-calendar.ts; Android draws the month only, for now.

/** A to-do on the calendar: on [day], at [time] there, or all day when [time] is null. */
data class CalendarEntry(
    val todoId: String,
    val title: String,
    val day: LocalDate,
    val time: LocalTime?,
    /** Its state, colored and named as on the list. */
    val state: TodoState,
    val done: Boolean,
    val overdue: Boolean,
    val high: Boolean,
    /** A reminder still to be pushed (the list's bell). */
    val reminding: Boolean,
) {
    val allDay: Boolean get() = time == null
}

/** The to-do on the calendar: at its due moment in [zone], or all day on its due day; null without either. */
fun Todo.calendarEntry(sessionLookup: (SessionKey) -> Session?, now: Instant, zone: ZoneId): CalendarEntry? {
    val at = parseTime(dueAt)?.atZone(zone)
    val day = at?.toLocalDate() ?: dueDay ?: return null
    return CalendarEntry(
        todoId = id,
        title = title,
        day = day,
        time = at?.toLocalTime(),
        state = state(sessionLookup),
        done = isDone,
        overdue = isOverdue(now, zone),
        high = priority == "high" && !isDone,
        reminding = !isDone && remind != null && remindedAt == null,
    )
}

/** The to-dos due from [from] up to (not including) [until], done ones only when [showDone]; each day's in [CalendarDrawOrder]. */
fun calendarEntries(
    todos: List<Todo>,
    sessionLookup: (SessionKey) -> Session?,
    showDone: Boolean,
    from: LocalDate,
    until: LocalDate,
    now: Instant,
    zone: ZoneId,
): Map<LocalDate, List<CalendarEntry>> = todos.asSequence()
    .filter { showDone || !it.isDone }
    .mapNotNull { it.calendarEntry(sessionLookup, now, zone) }
    .filter { !it.day.isBefore(from) && it.day.isBefore(until) }
    .groupBy { it.day }
    .mapValues { (_, day) -> day.sortedWith(CalendarDrawOrder) }

/** Open to-dos without a due date: not on the calendar, counted under it. */
fun undatedCount(todos: List<Todo>): Int = todos.count { !it.isDone && parseTime(it.dueAt) == null && it.dueDay == null }

/** All-day first, then by time; at one time open before done, then by title. */
val CalendarDrawOrder: Comparator<CalendarEntry> =
    compareBy<CalendarEntry>({ !it.allDay }, { it.time }, { it.done }, { it.title }, { it.todoId })

/** What a day of the month shows in [lines] lines: all of them when they fit, else a line fewer and how many more. */
data class DayFit<T>(val shown: List<T>, val more: Int)

fun <T> fitDay(entries: List<T>, lines: Int): DayFit<T> {
    if (entries.size <= lines || lines <= 0) return DayFit(entries, 0)
    val shown = entries.take(lines - 1)
    return DayFit(shown, entries.size - shown.size)
}

/** The time an all-day to-do takes when it is given one ("Change Due Date…" with All day off), as iOS `TodoPlanDraft`. */
val DefaultDueTime: LocalTime = LocalTime.of(18, 0)

/** A to-do dropped on [day] of the month (#2045): at the same time there in [zone]; an all-day one stays all day. */
fun CalendarEntry.dropOnDay(day: LocalDate, zone: ZoneId): TodoDue =
    time?.let { TodoDue.At(day.atTime(it).atZone(zone).toInstant()) } ?: TodoDue.Day(day)

/** The due date a drop sends: null when it lands where the to-do already is (nothing is sent). */
fun Todo.moveTo(due: TodoDue): TodoDue? = due.takeIf { it != this.due }

/**
 * The day of the month grid under a point, as an index into its days (0 is the first shown): [x] across the grid's
 * [width] (seven days a row, from the right when [rtl]), [y] down its rows of [rowHeight] (the scrolled-away part
 * counted); null off the grid.
 */
fun monthDropIndex(x: Float, y: Float, width: Float, rowHeight: Float, rows: Int, rtl: Boolean = false): Int? {
    if (width <= 0f || rowHeight <= 0f || x < 0f || x >= width || y < 0f) return null
    val row = (y / rowHeight).toInt()
    if (row >= rows) return null
    val column = (x / (width / 7)).toInt().coerceIn(0, 6)
    return row * 7 + if (rtl) 6 - column else column
}

/** Which way a month taller than its space scrolls while a dragged to-do is within [edge] of the top or bottom of the [height] in sight: -1 up, 1 down, 0 not. */
fun edgeScroll(y: Float, height: Float, edge: Float): Int = when {
    y < edge -> -1
    y > height - edge -> 1
    else -> 0
}

/** The days the month grid shows: the whole weeks [month] touches, weeks starting on [first]. */
fun monthDays(month: YearMonth, first: DayOfWeek): List<LocalDate> {
    val from = month.atDay(1).with(TemporalAdjusters.previousOrSame(first))
    val to = month.atEndOfMonth().with(TemporalAdjusters.nextOrSame(first.minus(1)))
    return generateSequence(from) { it.plusDays(1) }.takeWhile { !it.isAfter(to) }.toList()
}

/** The weekdays in the order the weeks start. */
fun weekdays(first: DayOfWeek): List<DayOfWeek> = List(7) { first.plus(it.toLong()) }

/**
 * The weekday weeks start on for [locale]: its region's (CLDR's week data, as the web reads it: `zh-CN` Monday, `en-US`
 * Sunday); a locale without a region goes by its language's usual one (`zh` → CN, Monday).
 */
fun weekStart(locale: Locale): DayOfWeek = regionWeekStart(locale.country.ifEmpty { likelyRegion(locale) })

fun regionWeekStart(region: String): DayOfWeek = when (region.uppercase(Locale.ROOT)) {
    in SUNDAY_FIRST -> DayOfWeek.SUNDAY
    in SATURDAY_FIRST -> DayOfWeek.SATURDAY
    else -> DayOfWeek.MONDAY
}

/** The region a language is most often written in, for the app's languages and their neighbors; "" for the rest (Monday). */
private fun likelyRegion(locale: Locale): String = when (locale.language) {
    "zh" -> if (locale.script == "Hant") "TW" else "CN"
    "ja" -> "JP"
    "en" -> "US"
    "ko" -> "KR"
    else -> ""
}

/** Regions whose weeks start on Sunday or Saturday (CLDR's week data, as web/src/lib/todo-calendar.ts lists them). */
private val SUNDAY_FIRST = ("AG AS BD BR BS BT BW BZ CA CO DM DO ET GT GU HK HN ID IL IN JM JP KE KH KR LA MH MM MO MT MX " +
    "MZ NI NP PA PE PH PK PR PT PY SA SG SV TH TT TW UM US VE VI WS YE ZA ZW").split(" ").toSet()
private val SATURDAY_FIRST = "AE AF BH DJ DZ EG IQ IR JO KW LY OM QA SD SY".split(" ").toSet()
