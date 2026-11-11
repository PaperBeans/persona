package persona.core.domain.tasks

import java.time.DayOfWeek
import java.time.Instant
import java.time.LocalDate
import java.time.LocalTime
import java.time.YearMonth
import java.time.ZoneId
import java.util.Locale
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import persona.core.contracts.Session
import persona.core.contracts.SessionKey
import persona.core.contracts.Todo
import persona.core.contracts.TodoAgent
import persona.core.contracts.TodoDue
import persona.core.contracts.TodoState

// The month's rules (#2046) and a drop on it (#2045), the cases of web/src/lib/todo-calendar.test.ts that the month needs.
class TodoCalendarTest {
    private val zone = ZoneId.of("Asia/Shanghai")
    private val now = Instant.parse("2026-11-05T09:30:00Z") // 17:30 on Nov 5 in zone
    private val none: (SessionKey) -> Session? = { null }

    private fun entry(todo: Todo) = todo.calendarEntry(none, now, zone)

    @Test fun aMomentIsOnItsLocalDayAtItsTime() {
        // 23:30 UTC on Nov 11 is 07:30 on Nov 12 here
        val e = entry(Todo("a", title = "A", dueAt = "2026-11-11T23:30:00Z"))!!
        assertEquals(LocalDate.of(2026, 11, 12), e.day)
        assertEquals(LocalTime.of(7, 30), e.time)
        assertFalse(e.allDay)
        assertFalse(e.overdue)
    }

    @Test fun aDayIsAllDayAndWithoutEitherNothing() {
        val e = entry(Todo("a", dueDate = "2026-11-12"))!!
        assertEquals(LocalDate.of(2026, 11, 12), e.day)
        assertTrue(e.allDay)
        assertNull(entry(Todo("b")))
        assertNull(entry(Todo("c", dueDate = "next week")))
    }

    @Test fun marksFollowTheList() {
        val late = entry(Todo("a", dueAt = "2026-11-05T08:00:00Z", priority = "high", remindAt = "2026-11-06T01:00:00Z"))!!
        assertTrue(late.overdue)
        assertTrue(late.high)
        assertTrue(late.reminding)
        assertEquals(TodoState.NotStarted, late.state)
        // done: never overdue, no flag, no bell
        val done = entry(Todo("b", dueAt = "2026-11-05T08:00:00Z", priority = "high", remindAt = "2026-11-06T01:00:00Z", doneAt = "2026-11-05T09:00:00Z"))!!
        assertTrue(done.done)
        assertFalse(done.overdue || done.high || done.reminding)
        assertEquals(TodoState.Done, done.state)
        // a reminder already pushed is no bell; an all-day to-do is late only once its day is over
        assertFalse(entry(Todo("c", dueDate = "2026-11-06", remindAt = "2026-11-06T01:00:00Z", remindedAt = "2026-11-06T01:00:01Z"))!!.reminding)
        assertFalse(entry(Todo("d", dueDate = "2026-11-05"))!!.overdue)
        assertTrue(entry(Todo("e", dueDate = "2026-11-04"))!!.overdue)
    }

    @Test fun theStateIsTheList() {
        val s = Session(machineId = "m", sessionId = "s", state = "needs_you")
        val e = Todo("a", dueDate = "2026-11-06", agents = listOf(TodoAgent("m", "s"))).calendarEntry({ if (it == s.key) s else null }, now, zone)!!
        assertEquals(TodoState.NeedsYou, e.state)
    }

    @Test fun entriesInRangeByDayInDrawOrder() {
        val todos = listOf(
            Todo("late", title = "B", dueAt = "2026-11-06T10:00:00Z"),
            Todo("early", title = "Z", dueAt = "2026-11-06T01:00:00Z"),
            Todo("allday", title = "Y", dueDate = "2026-11-06"),
            Todo("done", title = "A", dueAt = "2026-11-06T01:00:00Z", doneAt = "2026-11-04T00:00:00Z"),
            Todo("before", dueDate = "2026-10-31"),
            Todo("after", dueDate = "2026-12-01"),
            Todo("undated"),
        )
        val all = calendarEntries(todos, none, true, LocalDate.of(2026, 11, 1), LocalDate.of(2026, 12, 1), now, zone)
        assertEquals(setOf(LocalDate.of(2026, 11, 6)), all.keys)
        // all day first, then by time; at one time open before done
        assertEquals(listOf("allday", "early", "done", "late"), all.getValue(LocalDate.of(2026, 11, 6)).map { it.todoId })
        val open = calendarEntries(todos, none, false, LocalDate.of(2026, 11, 1), LocalDate.of(2026, 12, 1), now, zone)
        assertEquals(listOf("allday", "early", "late"), open.getValue(LocalDate.of(2026, 11, 6)).map { it.todoId })
    }

    @Test fun undatedCountsOpenOnesOnly() {
        assertEquals(1, undatedCount(listOf(Todo("a"), Todo("b", doneAt = "2026-11-01T00:00:00Z"), Todo("c", dueDate = "2026-11-06"),
            Todo("d", dueAt = "2026-11-06T01:00:00Z"))))
    }

    @Test fun aDayFitsItsLinesOrALineFewerAndMore() {
        assertEquals(DayFit(listOf(1, 2, 3), 0), fitDay(listOf(1, 2, 3), 3))
        assertEquals(DayFit(listOf(1, 2), 3), fitDay(listOf(1, 2, 3, 4, 5), 3))
        assertEquals(DayFit(emptyList<Int>(), 0), fitDay(emptyList<Int>(), 3))
    }

    @Test fun theMonthIsTheWholeWeeksItTouches() {
        // November 2026 starts on a Sunday and ends on a Monday
        val monday = monthDays(YearMonth.of(2026, 11), DayOfWeek.MONDAY)
        assertEquals(LocalDate.of(2026, 10, 26), monday.first())
        assertEquals(LocalDate.of(2026, 12, 6), monday.last())
        assertEquals(42, monday.size)
        val sunday = monthDays(YearMonth.of(2026, 11), DayOfWeek.SUNDAY)
        assertEquals(LocalDate.of(2026, 11, 1), sunday.first())
        assertEquals(LocalDate.of(2026, 12, 5), sunday.last())
        assertEquals(35, sunday.size)
        // February 2027 from Monday: exactly four weeks
        assertEquals(28, monthDays(YearMonth.of(2027, 2), DayOfWeek.MONDAY).size)
        assertEquals(listOf(DayOfWeek.SUNDAY, DayOfWeek.MONDAY), weekdays(DayOfWeek.SUNDAY).take(2))
        assertEquals(DayOfWeek.SATURDAY, weekdays(DayOfWeek.SUNDAY).last())
    }

    @Test fun weeksStartWhereTheRegionStartsThem() {
        assertEquals(DayOfWeek.MONDAY, weekStart(Locale.forLanguageTag("zh-CN")))
        assertEquals(DayOfWeek.MONDAY, weekStart(Locale.forLanguageTag("zh")))
        assertEquals(DayOfWeek.MONDAY, weekStart(Locale.forLanguageTag("zh-Hans")))
        assertEquals(DayOfWeek.SUNDAY, weekStart(Locale.forLanguageTag("zh-Hant")))
        assertEquals(DayOfWeek.SUNDAY, weekStart(Locale.forLanguageTag("en-US")))
        assertEquals(DayOfWeek.MONDAY, weekStart(Locale.forLanguageTag("en-GB")))
        assertEquals(DayOfWeek.SUNDAY, weekStart(Locale.forLanguageTag("ja")))
        assertEquals(DayOfWeek.MONDAY, weekStart(Locale.forLanguageTag("de-DE")))
        assertEquals(DayOfWeek.SATURDAY, weekStart(Locale.forLanguageTag("ar-EG")))
    }

    // #2045, as the web's dropOnDay / dropPatch
    @Test fun aDropOnAnotherDayKeepsTheTimeAndAllDayStaysAllDay() {
        // 18:00 on Nov 12 here
        val at = Todo("a", title = "A", dueAt = "2026-11-12T10:00:00Z")
        val moved = entry(at)!!.dropOnDay(LocalDate.of(2026, 11, 19), zone)
        assertEquals(TodoDue.At(Instant.parse("2026-11-19T10:00:00Z")), moved)
        assertEquals(moved, at.moveTo(moved))
        // seconds kept, as the web keeps them
        val odd = Todo("b", dueAt = "2026-11-12T10:00:30Z")
        assertEquals(TodoDue.At(Instant.parse("2026-11-03T10:00:30Z")), entry(odd)!!.dropOnDay(LocalDate.of(2026, 11, 3), zone))
        val day = Todo("c", dueDate = "2026-11-12")
        assertEquals(TodoDue.Day(LocalDate.of(2026, 12, 1)), entry(day)!!.dropOnDay(LocalDate.of(2026, 12, 1), zone))
        // done ones move too
        val done = Todo("d", dueDate = "2026-11-12", doneAt = "2026-11-04T10:00:00Z")
        assertEquals(TodoDue.Day(LocalDate.of(2026, 11, 13)), done.moveTo(entry(done)!!.dropOnDay(LocalDate.of(2026, 11, 13), zone)))
    }

    @Test fun aDropWhereItIsSendsNothing() {
        val at = Todo("a", dueAt = "2026-11-12T10:00:00Z")
        assertNull(at.moveTo(entry(at)!!.dropOnDay(LocalDate.of(2026, 11, 12), zone)))
        val day = Todo("b", dueDate = "2026-11-12")
        assertNull(day.moveTo(entry(day)!!.dropOnDay(LocalDate.of(2026, 11, 12), zone)))
        // the same day, but all day instead of at a time, is a change
        assertEquals(TodoDue.Day(LocalDate.of(2026, 11, 12)), at.moveTo(TodoDue.Day(LocalDate.of(2026, 11, 12))))
    }

    @Test fun aMovedTodoIsDueByTheOneFieldItWasMovedTo() {
        val at = Todo("a", dueDate = "2026-11-12").withDue(TodoDue.At(Instant.parse("2026-11-19T10:00:00Z")))
        assertEquals("2026-11-19T10:00:00Z", at.dueAt)
        assertNull(at.dueDate)
        val day = Todo("b", dueAt = "2026-11-12T10:00:00Z").withDue(TodoDue.Day(LocalDate.of(2026, 11, 19)))
        assertNull(day.dueAt)
        assertEquals("2026-11-19", day.dueDate)
        assertEquals(TodoDue.Day(LocalDate.of(2026, 11, 19)), day.due)
        assertNull(Todo("c").due)
    }

    @Test fun theDayUnderAPointOfTheMonth() {
        // 700 wide (100 a day), rows 90 high, six of them
        assertEquals(0, monthDropIndex(0f, 0f, 700f, 90f, 6))
        assertEquals(6, monthDropIndex(699f, 89f, 700f, 90f, 6))
        assertEquals(7 + 3, monthDropIndex(350f, 100f, 700f, 90f, 6))
        assertEquals(35 + 6, monthDropIndex(650f, 539f, 700f, 90f, 6))
        // right to left, the first day is at the right
        assertEquals(6, monthDropIndex(0f, 0f, 700f, 90f, 6, rtl = true))
        // off the grid
        assertNull(monthDropIndex(-1f, 10f, 700f, 90f, 6))
        assertNull(monthDropIndex(700f, 10f, 700f, 90f, 6))
        assertNull(monthDropIndex(10f, -1f, 700f, 90f, 6))
        assertNull(monthDropIndex(10f, 540f, 700f, 90f, 6))
    }

    @Test fun theMonthScrollsNearItsEdgesWhileDragging() {
        assertEquals(-1, edgeScroll(10f, 500f, 40f))
        assertEquals(0, edgeScroll(250f, 500f, 40f))
        assertEquals(1, edgeScroll(470f, 500f, 40f))
        // below the part in sight it keeps going down
        assertEquals(1, edgeScroll(600f, 500f, 40f))
    }
}
