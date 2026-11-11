package persona.feature.agents

import android.text.format.DateFormat
import androidx.compose.foundation.layout.Column
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.res.stringResource
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay
import persona.core.contracts.Todo
import persona.core.contracts.TodoDue
import persona.core.contracts.TodoPlan
import persona.core.designsystem.*
import persona.core.designsystem.icons.PersonaIcons
import persona.core.domain.tasks.TasksStore
import java.time.*
import java.time.format.DateTimeFormatter
import java.time.temporal.ChronoUnit

/**
 * What the detail page's due date, reminder and priority hold (#4636, iOS `TodoPlanDraft`): a date stays in its picker
 * while its switch is off. A new due date starts tomorrow at 18:00 (all day), a new reminder at the next full hour.
 */
internal data class TodoPlanDraft(
    val hasDue: Boolean = false,
    val allDay: Boolean = true,
    val due: LocalDateTime,
    val hasRemind: Boolean = false,
    val remind: LocalDateTime,
    val priority: String = "",
) {
    /** The PATCH that makes the to-do what this draft says (the dates of a switch that is off don't count). */
    fun plan(zone: ZoneId): TodoPlan = TodoPlan(
        due = when {
            !hasDue -> null
            allDay -> TodoDue.Day(due.toLocalDate())
            else -> TodoDue.At(due.atZone(zone).toInstant())
        },
        remindAt = remind.atZone(zone).toInstant().takeIf { hasRemind },
        priority = priority,
    )

    companion object {
        fun of(todo: Todo, now: ZonedDateTime): TodoPlanDraft {
            val zone = now.zone
            val evening = now.toLocalDate().plusDays(1).atTime(18, 0)
            val nextHour = now.toLocalDateTime().truncatedTo(ChronoUnit.HOURS).plusHours(1)
            val at = todo.dueAt?.let { runCatching { Instant.parse(it) }.getOrNull() }
            val day = todo.dueDay
            return TodoPlanDraft(
                hasDue = at != null || day != null,
                allDay = at == null,
                due = at?.atZone(zone)?.toLocalDateTime() ?: day?.atTime(18, 0) ?: evening,
                hasRemind = todo.remind != null,
                remind = todo.remind?.atZone(zone)?.toLocalDateTime() ?: nextHour,
                priority = todo.priority.orEmpty(),
            )
        }
    }
}

/**
 * The detail page's due date, reminder and priority (iOS `TodoPlanSection`). A change is saved shortly after it is made;
 * the controls follow the to-do again once nothing is being saved.
 */
@Composable
internal fun TodoPlanSection(todo: Todo, tasks: TasksStore?, onFailure: (Throwable) -> Unit) {
    val zone = ZoneId.systemDefault()
    val fromTodo = remember(todo) { TodoPlanDraft.of(todo, ZonedDateTime.now(zone)) }
    var draft by remember(todo.id) { mutableStateOf(fromTodo) }
    var saving by remember(todo.id) { mutableStateOf(false) }
    LaunchedEffect(fromTodo) { if (!saving) draft = fromTodo }
    LaunchedEffect(draft) {
        if (tasks == null || draft.plan(zone) == fromTodo.plan(zone)) return@LaunchedEffect
        delay(400) // a picker sends every step; only the last is saved
        saving = true
        try { tasks.setTodoPlan(todo.id, draft.plan(zone)) }
        catch (e: CancellationException) { throw e }
        catch (e: Exception) { onFailure(e); draft = fromTodo }
        finally { saving = false }
    }
    val locale = LocalConfiguration.current.locales[0]
    // the year only when it is not this one, as iOS TodoPlanText
    fun date(d: LocalDateTime) = DateTimeFormatter.ofPattern(DateFormat.getBestDateTimePattern(locale,
        if (d.year == LocalDate.now(zone).year) "EEEMMMd" else "yEEEMMMd"), locale).format(d)
    fun time(d: LocalDateTime) = DateTimeFormatter.ofPattern(DateFormat.getBestDateTimePattern(locale, "jjmm"), locale).format(d)
    var picking by remember { mutableStateOf<String?>(null) }
    val enabled = tasks != null
    Column {
        JSwitchRow(stringResource(R.string.agents_todo_plan_due), draft.hasDue, { draft = draft.copy(hasDue = it) },
            leadingIcon = PersonaIcons.calendarView, enabled = enabled)
        if (draft.hasDue) {
            JSwitchRow(stringResource(R.string.agents_todo_plan_all_day), draft.allDay, { draft = draft.copy(allDay = it) }, enabled = enabled)
            JRow(stringResource(R.string.agents_todo_plan_date), value = date(draft.due), enabled = enabled, onClick = { picking = "due-date" })
            if (!draft.allDay) JRow(stringResource(R.string.agents_todo_plan_time), value = time(draft.due), enabled = enabled, onClick = { picking = "due-time" })
        }
        JSwitchRow(stringResource(R.string.agents_todo_plan_remind), draft.hasRemind, { draft = draft.copy(hasRemind = it) },
            leadingIcon = PersonaIcons.reminder, enabled = enabled)
        if (draft.hasRemind) {
            JRow(stringResource(R.string.agents_todo_plan_date), value = date(draft.remind), enabled = enabled, onClick = { picking = "remind-date" })
            JRow(stringResource(R.string.agents_todo_plan_time), value = time(draft.remind), enabled = enabled, onClick = { picking = "remind-time" })
            val sent = todo.remindedAt?.let { runCatching { Instant.parse(it) }.getOrNull() }
            if (sent != null && todo.remind == draft.remind.atZone(zone).toInstant())
                Text(stringResource(R.string.agents_todo_plan_reminded, date(sent.atZone(zone).toLocalDateTime()) + " " + time(sent.atZone(zone).toLocalDateTime())),
                    style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2)
        }
        PriorityRow(draft.priority, enabled) { draft = draft.copy(priority = it) }
    }
    when (picking) {
        "due-date" -> JDatePickerDialog(draft.due.toLocalDate(), { draft = draft.copy(due = it.atTime(draft.due.toLocalTime())) }, { picking = null })
        "due-time" -> JTimePickerDialog(draft.due.toLocalTime(), { draft = draft.copy(due = draft.due.toLocalDate().atTime(it)) }, { picking = null },
            stringResource(R.string.agents_todo_plan_due))
        "remind-date" -> JDatePickerDialog(draft.remind.toLocalDate(), { draft = draft.copy(remind = it.atTime(draft.remind.toLocalTime())) },
            { picking = null }, earliest = LocalDate.now(zone).takeIf { draft.remind.toLocalDate() >= it })
        "remind-time" -> JTimePickerDialog(draft.remind.toLocalTime(), { draft = draft.copy(remind = draft.remind.toLocalDate().atTime(it)) }, { picking = null },
            stringResource(R.string.agents_todo_plan_remind_at))
    }
}

@Composable
private fun PriorityRow(value: String, enabled: Boolean, onSelect: (String) -> Unit) {
    var open by remember { mutableStateOf(false) }
    val options = listOf("high" to R.string.agents_todo_plan_high, "" to R.string.agents_todo_plan_normal, "low" to R.string.agents_todo_plan_low)
    JRow(stringResource(R.string.agents_todo_plan_priority), leadingIcon = PersonaIcons.highPriority,
        value = stringResource(options.firstOrNull { it.first == value }?.second ?: R.string.agents_todo_plan_normal), enabled = enabled, onClick = { open = true })
    if (open) JActionSheet(options.map { (id, label) -> JAction(stringResource(label), checked = id == value, onClick = { onSelect(id) }) },
        { open = false }, title = stringResource(R.string.agents_todo_plan_priority))
}
