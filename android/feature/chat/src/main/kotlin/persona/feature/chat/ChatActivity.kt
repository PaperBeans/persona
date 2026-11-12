package persona.feature.chat

import android.content.res.Resources
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import kotlinx.coroutines.delay
import persona.core.contracts.BackgroundTask
import persona.core.contracts.LoopInfo
import persona.core.contracts.Session
import persona.core.designsystem.AppStyle
import persona.core.designsystem.JActivityPill
import persona.core.designsystem.JButton
import persona.core.designsystem.JButtonKind
import persona.core.designsystem.JButtonSize
import persona.core.designsystem.JCard
import persona.core.designsystem.JDialog
import persona.core.designsystem.JIcon
import persona.core.designsystem.JSheet
import persona.core.designsystem.LocalizedFormats
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.Tokens
import persona.core.designsystem.icons.PersonaIcons
import java.time.Duration
import java.time.Instant
import java.time.ZoneId

/**
 * A loop and background work in an Agent's chat (#4339 C8, iOS `ActivityChips`, `ActivitySheet`, `LoopStrip` and
 * `BackgroundTasksStrip`): on Glass a pill at the end of the header's second row opens a sheet that stops the loop and
 * opens a task's output. Brief and Board keep their chips above the composer, which open the status sheet.
 */
internal object ChatActivity {
    /** Glass shows them in the header of an Agent's chat; the other styles and Persona's conversations do not. */
    fun inHeader(style: AppStyle, topBar: Boolean, render: ChatRenderStyle): Boolean =
        style == AppStyle.Glass && topBar && render != ChatRenderStyle.Assistant

    /** Whether there is anything for the pill: a loop, or background tasks (workflows are listed on their own). */
    fun shown(session: Session?): Boolean = session != null && (session.loop != null || session.backgroundTasks.isNotEmpty())

    /**
     * What Stop Loop sends (iOS `AppModel.stopLoop`, the same words): nothing outside the agent can cancel its
     * scheduled wake-up or its cron job, so the agent is asked to, and it stops scheduling.
     */
    const val STOP_LOOP = "停止当前的 /loop：调用 ScheduleWakeup(stop: true) 结束循环（如果用 CronCreate 建过定时任务，用 CronDelete 删掉），" + // i18n-ignore: a prompt sent to the agent, iOS's words
        "不要再安排下一次唤醒，回复一句「循环已停止」即可。" // i18n-ignore: a prompt sent to the agent, iOS's words

    fun next(loop: LoopInfo): Instant? = loop.nextAt?.let { runCatching { Instant.parse(it) }.getOrNull() }

    /** The task, without the "/loop" command word (iOS `LoopInfo.task`). */
    fun task(loop: LoopInfo): String {
        val prompt = loop.prompt.orEmpty().trim()
        return if (prompt.startsWith("/loop")) prompt.removePrefix("/loop").trim() else prompt
    }

    /** "Every 30 minutes" / "Cron 33 17 * * *" (iOS `LoopInfo.cadence`); null when the hub says neither. */
    fun cadence(res: Resources, loop: LoopInfo): String? {
        if (loop.kind == "cron") loop.cron?.takeIf(String::isNotBlank)?.let { return res.getString(R.string.chat_activity_cron, it) }
        val s = loop.intervalSeconds?.takeIf { it > 0 } ?: return null
        return when {
            s < 120 -> res.getQuantityString(R.plurals.chat_activity_every_seconds, s, s)
            s < 7200 -> res.getQuantityString(R.plurals.chat_activity_every_minutes, s / 60, s / 60)
            else -> res.getQuantityString(R.plurals.chat_activity_every_hours, s / 3600, s / 3600)
        }
    }

    /** The pill's loop words (iOS `shortLoopLabel`): the next time, Running once it is due, else Loop. */
    fun shortLabel(res: Resources, loop: LoopInfo, now: Instant, zone: ZoneId = ZoneId.systemDefault()): String {
        val next = next(loop)
        return when {
            next != null && next.isAfter(now) -> time(res, next, zone)
            loop.nextAt != null -> res.getString(R.string.chat_activity_running)
            else -> res.getString(R.string.chat_activity_loop)
        }
    }

    /** The loop in full words, for the screen reader (iOS `loopLabel`): "Next 14:05", "Loop running" or its cadence. */
    fun label(res: Resources, loop: LoopInfo, now: Instant, zone: ZoneId = ZoneId.systemDefault()): String {
        val next = next(loop)
        return when {
            next != null && next.isAfter(now) -> res.getString(R.string.chat_activity_next, time(res, next, zone))
            loop.nextAt != null -> res.getString(R.string.chat_activity_loop_running)
            else -> cadence(res, loop) ?: res.getString(R.string.chat_activity_looping)
        }
    }

    /** What the pill says to the screen reader (iOS `ActivityChips.a11yLabel`): "Looping, Next 14:05, 2 background tasks, tap to view". */
    fun spoken(res: Resources, loop: LoopInfo?, background: Int, now: Instant, zone: ZoneId = ZoneId.systemDefault()): String {
        val parts = listOfNotNull(
            loop?.let { res.getString(R.string.chat_activity_looping_spoken, label(res, it, now, zone)) },
            background.takeIf { it > 0 }?.let { res.getQuantityString(R.plurals.chat_activity_background_count, it, it) },
        )
        return res.getString(R.string.chat_activity_tap_to_view, parts.joinToString(res.getString(R.string.chat_activity_list_separator)))
    }

    /** The sheet's title (iOS `ActivitySheet`): Loop, Background Task, or both. */
    fun title(res: Resources, loop: Boolean, background: Boolean): String = res.getString(when {
        loop && !background -> R.string.chat_activity_title_loop
        !loop -> R.string.chat_activity_title_background
        else -> R.string.chat_activity_title_both
    })

    /** "4 minutes left" / "1 h 5 min left" / "2 hours left" (iOS `LoopStrip.until`). */
    fun until(res: Resources, date: Instant, now: Instant): String {
        val m = maxOf(1L, Math.ceil(Duration.between(now, date).toMillis() / 60_000.0).toLong()).toInt()
        return when {
            m < 60 -> res.getQuantityString(R.plurals.chat_activity_minutes_left, m, m)
            m % 60 > 0 -> res.getString(R.string.chat_activity_hours_minutes_left, m / 60, m % 60)
            else -> res.getQuantityString(R.plurals.chat_activity_hours_left, m / 60, m / 60)
        }
    }

    /** How long a task has run: "42 s", "3m 5s", "1h 12m" (iOS `BackgroundTasksStrip.elapsed`). */
    fun elapsed(res: Resources, start: Instant, now: Instant): String {
        val s = maxOf(0L, Duration.between(start, now).seconds).toInt()
        return when {
            s < 60 -> res.getString(R.string.chat_activity_seconds, s)
            s < 3600 -> res.getString(R.string.chat_activity_minutes_seconds, s / 60, s % 60)
            else -> res.getString(R.string.chat_activity_hours_minutes, s / 3600, s % 3600 / 60)
        }
    }

    /** A task by what it is for (iOS `BackgroundTask.title`): its description, its command's first line, else its kind. */
    fun taskTitle(res: Resources, task: BackgroundTask): String =
        task.description?.takeIf(String::isNotBlank)
            ?: task.command?.lineSequence()?.firstOrNull { it.isNotBlank() }?.trim()
            ?: res.getString(if (task.kind == "agent") R.string.chat_activity_subagent else R.string.chat_activity_background_command)

    private fun time(res: Resources, at: Instant, zone: ZoneId): String =
        LocalizedFormats.format(at, LocalizedFormats.TIME, res.configuration.locales[0], zone)
}

/** The clock the pill and the sheet read, every [millis] (iOS `TimelineView(.periodic)`). */
@Composable
private fun ticking(millis: Long): Instant {
    val now by produceState(Instant.now(), millis) { while (true) { delay(millis); value = Instant.now() } }
    return now
}

/** The pill at the end of the header's second row (iOS `ActivityChips`). */
@Composable
internal fun ChatActivityPill(session: Session, onOpen: () -> Unit) {
    val res = LocalResources.current
    val now = ticking(30_000)
    val loop = session.loop
    val background = session.backgroundTasks.size
    JActivityPill(loop?.let { ChatActivity.shortLabel(res, it, now) }, background,
        ChatActivity.spoken(res, loop, background, now), onOpen)
}

/** The header's second row with nothing but the pill, held at its trailing end as beside the tabs. */
@Composable
internal fun ChatActivityRow(pill: @Composable () -> Unit) {
    Row(Modifier.fillMaxWidth().padding(horizontal = Tokens.Space.l, vertical = Tokens.Space.xs),
        horizontalArrangement = Arrangement.End, verticalAlignment = Alignment.CenterVertically) { pill() }
}

/**
 * The pill opened (iOS `ActivitySheet`): the loop with Stop Loop, which asks first and then sends the agent
 * [ChatActivity.STOP_LOOP] ([onStop]; null: this chat can't send), and every background task, a tap on one with an
 * output file opening it ([onOpenOutput]; null: files can't open here).
 */
@Composable
internal fun ChatActivitySheet(
    session: Session, onStop: (() -> Unit)?, onOpenOutput: ((String) -> Unit)?, onDismiss: () -> Unit,
) {
    val res = LocalResources.current
    val loop = session.loop
    val tasks = session.backgroundTasks
    JSheet(onDismiss = onDismiss, title = ChatActivity.title(res, loop != null, tasks.isNotEmpty()), scrollable = true) {
        Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.m)) {
            if (loop != null) LoopStrip(loop, onStop)
            if (tasks.isNotEmpty()) BackgroundStrip(tasks, onOpenOutput)
        }
        Spacer(Modifier.height(Tokens.Space.l))
    }
}

/** The loop driving the agent (iOS `LoopStrip`, open): what it repeats, how often, when it wakes next, and Stop Loop. */
@Composable
private fun LoopStrip(loop: LoopInfo, onStop: (() -> Unit)?) {
    val res = LocalResources.current
    val palette = PersonaTheme.palette
    var open by rememberSaveable { mutableStateOf(true) }
    var confirming by remember { mutableStateOf(false) }
    val now = ticking(30_000)
    JCard(padding = Tokens.Space.flush) {
        Column(Modifier.padding(horizontal = Tokens.Space.l, vertical = Tokens.Space.m), verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
            Row(Modifier.fillMaxWidth().clickable(role = Role.Button, onClickLabel = stringResource(if (open) R.string.chat_activity_collapse else R.string.chat_activity_expand)) { open = !open },
                verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                JIcon(PersonaIcons.inProgress, null, size = Tokens.Size.markS, tint = if (palette.dark) Tokens.Common.Dark.loop else Tokens.Common.Light.loop)
                Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(Tokens.Space.xxs)) {
                    Text(ChatActivity.cadence(res, loop)?.let { stringResource(R.string.chat_activity_looping_cadence, it) } ?: stringResource(R.string.chat_activity_looping),
                        style = PersonaTheme.type.caption.copy(fontWeight = FontWeight.SemiBold), color = palette.ink, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    ChatActivity.next(loop)?.let { next ->
                        Text(if (next.isAfter(now)) stringResource(R.string.chat_activity_next_wakeup,
                                LocalizedFormats.format(next, LocalizedFormats.TIME, res.configuration.locales[0], ZoneId.systemDefault()),
                                ChatActivity.until(res, next, now))
                            else stringResource(R.string.chat_activity_running_round),
                            style = PersonaTheme.type.micro, color = palette.ink2, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    }
                }
                JButton(stringResource(R.string.chat_activity_stop), { confirming = true }, kind = JButtonKind.Destructive,
                    size = JButtonSize.Small, enabled = onStop != null)
            }
            if (open) {
                ChatActivity.task(loop).takeIf(String::isNotBlank)?.let {
                    SelectionContainer { Text(it, style = PersonaTheme.type.callout, color = palette.ink2) }
                }
                loop.reason?.takeIf(String::isNotBlank)?.let { reason ->
                    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
                        JIcon(PersonaIcons.agentChat, null, size = Tokens.Size.markS, tint = palette.ink3)
                        Text(reason, style = PersonaTheme.type.micro, color = palette.ink3)
                    }
                }
            }
        }
    }
    if (confirming) JDialog(onDismiss = { confirming = false }, title = stringResource(R.string.chat_activity_stop_title),
        text = stringResource(R.string.chat_activity_stop_text),
        confirm = stringResource(R.string.chat_activity_stop) to { confirming = false; onStop?.invoke() }, destructive = true)
}

/** Every background task (iOS `BackgroundTasksStrip`, open): what it runs, for how long, and its output when it has one. */
@Composable
private fun BackgroundStrip(tasks: List<BackgroundTask>, onOpenOutput: ((String) -> Unit)?) {
    val res = LocalResources.current
    val palette = PersonaTheme.palette
    var open by rememberSaveable { mutableStateOf(true) }
    val count = pluralStringResource(R.plurals.chat_activity_background_count, tasks.size, tasks.size)
    JCard(padding = Tokens.Space.flush) {
        Column(Modifier.padding(horizontal = Tokens.Space.l, vertical = Tokens.Space.m), verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
            val state = stringResource(if (open) R.string.chat_activity_collapse else R.string.chat_activity_expand)
            Row(Modifier.fillMaxWidth().clickable(role = Role.Button) { open = !open }.semantics { contentDescription = "$count, $state" },
                verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                JIcon(PersonaIcons.backgroundTask, null, size = Tokens.Size.markS, tint = palette.working.dot)
                Text(count, style = PersonaTheme.type.caption.copy(fontWeight = FontWeight.SemiBold), color = palette.ink, maxLines = 1)
                if (!open) Text(ChatActivity.taskTitle(res, tasks.first()), Modifier.weight(1f, fill = false), style = PersonaTheme.type.caption,
                    color = palette.ink2, maxLines = 1, overflow = TextOverflow.Ellipsis)
                Spacer(Modifier.weight(1f))
                JIcon(if (open) PersonaIcons.collapse else PersonaIcons.expand, null, size = Tokens.Size.markS, tint = palette.ink2)
            }
            if (open) {
                val now = ticking(1_000)
                tasks.forEach { task -> TaskRow(task, now, onOpenOutput) }
            }
        }
    }
}

@Composable
private fun TaskRow(task: BackgroundTask, now: Instant, onOpenOutput: ((String) -> Unit)?) {
    val res = LocalResources.current
    val palette = PersonaTheme.palette
    val title = ChatActivity.taskTitle(res, task)
    val output = task.outputFile?.takeIf(String::isNotBlank)?.takeIf { onOpenOutput != null }
    Row(
        Modifier.fillMaxWidth().clip(RoundedCornerShape(PersonaTheme.shapes.card)).background(palette.sunken)
            .then(if (output != null) Modifier.clickable(role = Role.Button, onClickLabel = stringResource(R.string.chat_activity_view_output)) { onOpenOutput?.invoke(output) } else Modifier)
            .padding(Tokens.Space.m),
        horizontalArrangement = Arrangement.spacedBy(Tokens.Space.m),
    ) {
        JIcon(if (task.kind == "agent") PersonaIcons.backgroundTask else PersonaIcons.terminal, null, size = Tokens.Size.markS, tint = palette.ink2)
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
            Text(title, style = PersonaTheme.type.callout.copy(fontWeight = FontWeight.Medium), color = palette.ink, maxLines = 1, overflow = TextOverflow.Ellipsis)
            task.command?.takeIf { it.isNotBlank() && it != title }?.let {
                Text(it, style = PersonaTheme.type.codeSmall, color = palette.ink2, maxLines = 2, overflow = TextOverflow.Ellipsis)
            }
        }
        Column(horizontalAlignment = Alignment.End, verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
            task.startedAt?.let { runCatching { Instant.parse(it) }.getOrNull() }?.let { start ->
                Text(ChatActivity.elapsed(res, start, now), style = PersonaTheme.type.micro, color = palette.ink2, maxLines = 1)
            }
            if (output != null) Text(stringResource(R.string.chat_activity_view_output),
                style = PersonaTheme.type.micro.copy(fontWeight = FontWeight.SemiBold), color = palette.accent, maxLines = 1)
        }
    }
}
