package persona.core.designsystem

import androidx.compose.ui.res.stringResource
import androidx.compose.animation.animateContentSize
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Text
import androidx.compose.material3.minimumInteractiveComponentSize
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import persona.core.designsystem.icons.PersonaIcons
import persona.core.designsystem.icons.PhIcons

/** How many rows an opened queue shows before it scrolls (iOS `QueueStrip.visibleRows`). */
const val QUEUE_VISIBLE_ROWS = 3

/**
 * How tall an opened queue's list may be, in pixels, from the height its rows take together (spacing included): about
 * [QUEUE_VISIBLE_ROWS] of them, all of them when there are fewer (iOS measures one row and multiplies). Null until it
 * was measured.
 */
fun queueListCap(rowsHeight: Int, count: Int): Int? =
    if (rowsHeight <= 0 || count <= 0) null else rowsHeight * minOf(count, QUEUE_VISIBLE_ROWS) / count

/**
 * Messages typed while the agent works and not taken yet, right above the composer (iOS QueueStrip): [title] says how
 * many and that they go in order ("2 queued · Claude will handle them in order"), then each one numbered, at most two
 * lines. Tapping one hands its index to [onItem] (its actions). While an operation runs ([busy]) a spinner shows and
 * nothing can be tapped; [enabled] false shows the queue without actions. A long queue scrolls under the title.
 *
 * Glass is iOS's strip: folded to the title line by default, which opens and folds it as iOS's disclosure does
 * ([initiallyExpanded] opens it from the start: catalog, screenshots); opened, each message sits in a well and about
 * [QUEUE_VISIBLE_ROWS] show before it scrolls. Its line carries [sendNow], a text button folded or open, for
 * [onSendNow] (iOS Send Now). Brief (a paper card) and the board (a bordered ticket) stay always open, without Send now
 * (the alignment plan's ground rules: no new alignment work outside Glass). In every style a row with a note in [notes]
 * (by index) says it in the caption size under its words (iOS's "Arrives when this chat is free" under a message
 * relayed from another conversation); without notes Brief and Board draw as before.
 */
@Composable
fun QueueStrip(
    title: String,
    items: List<String>,
    onItem: (Int) -> Unit,
    modifier: Modifier = Modifier,
    busy: Boolean = false,
    enabled: Boolean = true,
    sendNow: String? = null,
    onSendNow: () -> Unit = {},
    notes: List<String?> = emptyList(),
    initiallyExpanded: Boolean = false,
) {
    if (items.isEmpty()) return
    if (PersonaTheme.style == AppStyle.Glass) GlassQueueStrip(title, items, onItem, modifier, busy, enabled, sendNow, onSendNow, notes, initiallyExpanded)
    else PlainQueueStrip(title, items, onItem, modifier, busy, enabled, notes)
}

/** iOS's strip: the title line in the label size, then the rows as wells, about three of them before it scrolls. */
@Composable
private fun GlassQueueStrip(
    title: String, items: List<String>, onItem: (Int) -> Unit, modifier: Modifier, busy: Boolean, enabled: Boolean,
    sendNow: String?, onSendNow: () -> Unit, notes: List<String?>, initiallyExpanded: Boolean,
) {
    var expanded by rememberSaveable { mutableStateOf(initiallyExpanded) }
    val spoken = stringResource(R.string.designsystem_with_state, title,
        stringResource(if (expanded) R.string.designsystem_expanded else R.string.designsystem_collapsed))
    val palette = PersonaTheme.palette
    val type = PersonaTheme.type
    val active = enabled && !busy
    val line = type.micro.copy(fontWeight = FontWeight.SemiBold)
    Column(
        modifier.fillMaxWidth().padding(horizontal = 10.dp.dense, vertical = Tokens.Space.xs)
            .floatingSurface(RoundedCornerShape(Tokens.Radius.panel)).animateContentSize(PersonaTheme.motion.size()),
    ) {
        Row(
            Modifier.fillMaxWidth().heightIn(min = Tokens.MinTouch.android)
                .clickable(role = Role.Button) { expanded = !expanded }.semantics { contentDescription = spoken }
                .padding(horizontal = Tokens.Space.m),
            verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.sm),
        ) {
            JIcon(PhIcons.Tray, null, size = Tokens.Space.l, tint = palette.ink2)
            Text(title, style = line, color = palette.ink2, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f))
            if (busy) JSpinner(Modifier.size(Tokens.Space.Standard.m), stroke = Tokens.Space.Standard.xxs)
            else sendNow?.let { SendNowButton(it, line, onSendNow) }
            JIcon(if (expanded) PersonaIcons.expand else PhIcons.CaretRight, null, size = Tokens.Space.m, tint = palette.ink3)
        }
        if (expanded) {
            var rowsHeight by remember { mutableIntStateOf(0) }
            val cap = queueListCap(rowsHeight, items.size)?.let { with(LocalDensity.current) { it.toDp() } }
            Box(
                Modifier.fillMaxWidth().padding(bottom = Tokens.Space.m).then(if (cap != null) Modifier.heightIn(max = cap) else Modifier)
                    .verticalScroll(rememberScrollState()),
            ) {
                Column(
                    Modifier.fillMaxWidth().padding(horizontal = Tokens.Space.m).onSizeChanged { rowsHeight = it.height },
                    verticalArrangement = Arrangement.spacedBy(Tokens.Space.sm),
                ) {
                    val itemActions = stringResource(R.string.designsystem_queue_actions)
                    items.forEachIndexed { index, text ->
                        val note = notes.getOrNull(index)
                        val spoken = if (note == null) stringResource(R.string.designsystem_queue_item, index + 1, text)
                            else stringResource(R.string.designsystem_queue_item_note, index + 1, text, note)
                        Row(
                            Modifier.fillMaxWidth().clip(RoundedCornerShape(Tokens.Radius.card)).background(palette.sunken)
                                .clickable(enabled = active, role = Role.Button, onClickLabel = itemActions) { onItem(index) }
                                .semantics(mergeDescendants = true) { contentDescription = spoken }
                                .padding(horizontal = Tokens.Space.m, vertical = Tokens.Space.s),
                            verticalAlignment = Alignment.Top, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s),
                        ) {
                            QueueNumber(index + 1)
                            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(Tokens.Space.xxs)) {
                                Text(text, style = type.callout, color = palette.ink, maxLines = 2, overflow = TextOverflow.Ellipsis)
                                note?.let { Text(it, style = type.caption, color = palette.ink2) }
                            }
                            if (enabled) JIcon(PersonaIcons.more, null, size = Tokens.Space.l, tint = palette.ink3,
                                modifier = Modifier.padding(top = Tokens.Space.xxs))
                        }
                    }
                }
            }
        }
    }
}

/** Brief and Board: their card and ticket as before, always open; a row's note under its words. */
@Composable
private fun PlainQueueStrip(
    title: String, items: List<String>, onItem: (Int) -> Unit, modifier: Modifier, busy: Boolean, enabled: Boolean, notes: List<String?>,
) {
    val palette = PersonaTheme.palette
    val style = PersonaTheme.style
    val shape = RoundedCornerShape(if (style == AppStyle.Brief) 20.dp.corner else 8.dp.corner)
    val outer = when (style) {
        AppStyle.Brief -> Modifier.padding(horizontal = 12.dp.dense, vertical = 4.dp.dense).clip(shape).background(palette.surface)
        else -> Modifier.padding(horizontal = 10.dp.dense, vertical = 4.dp.dense).clip(shape).background(palette.surface).border(1.dp, palette.line, shape)
    }
    val active = enabled && !busy
    Column(modifier.fillMaxWidth().then(outer).padding(top = 10.dp.dense, bottom = 4.dp.dense)) {
        Row(Modifier.fillMaxWidth().padding(horizontal = 14.dp.dense), verticalAlignment = Alignment.CenterVertically) {
            JIcon(PhIcons.Tray, null, size = 16.dp, tint = palette.ink2)
            Spacer(Modifier.width(8.dp.dense))
            Text(title, style = PersonaTheme.type.caption, color = palette.ink2, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f))
            if (busy) JSpinner(Modifier.size(14.dp), stroke = 1.8.dp)
        }
        Column(Modifier.fillMaxWidth().heightIn(max = 184.dp).verticalScroll(rememberScrollState()).padding(top = 4.dp.dense)) {
            val itemActions = stringResource(R.string.designsystem_queue_actions)
            items.forEachIndexed { index, text ->
                val note = notes.getOrNull(index)
                val spoken = if (note == null) stringResource(R.string.designsystem_queue_item, index + 1, text)
                    else stringResource(R.string.designsystem_queue_item_note, index + 1, text, note)
                if (index > 0) JDivider(Modifier.padding(start = 50.dp.dense, end = 14.dp.dense))
                Row(
                    Modifier.fillMaxWidth().heightIn(min = 44.dp.row)
                        .clickable(enabled = active, role = Role.Button, onClickLabel = itemActions) { onItem(index) }
                        .semantics(mergeDescendants = true) { contentDescription = spoken }
                        .padding(horizontal = 14.dp.dense, vertical = 8.dp.dense),
                    verticalAlignment = Alignment.Top,
                ) {
                    QueueNumber(index + 1)
                    Spacer(Modifier.width(12.dp.dense))
                    val words = Modifier.weight(1f).padding(top = 1.dp.dense)
                    if (note == null) Text(text, style = PersonaTheme.type.callout, color = palette.ink, maxLines = 2, overflow = TextOverflow.Ellipsis,
                        modifier = words)
                    else Column(words, verticalArrangement = Arrangement.spacedBy(Tokens.Space.xxs)) {
                        Text(text, style = PersonaTheme.type.callout, color = palette.ink, maxLines = 2, overflow = TextOverflow.Ellipsis)
                        Text(note, style = PersonaTheme.type.caption, color = palette.ink2)
                    }
                    if (enabled) {
                        Spacer(Modifier.width(8.dp.dense))
                        JIcon(PhIcons.DotsThree, null, size = 18.dp, tint = palette.ink3, modifier = Modifier.padding(top = 2.dp.dense))
                    }
                }
            }
        }
    }
}

/** Send Now on Glass's title line: the accent's words, as iOS's plain button, with the platform's touch target around them. */
@Composable
private fun SendNowButton(label: String, style: TextStyle, onClick: () -> Unit) {
    Box(
        Modifier.minimumInteractiveComponentSize().clip(PersonaTheme.shapes.controlShape).clickable(role = Role.Button, onClick = onClick)
            .padding(horizontal = Tokens.Space.xs),
        contentAlignment = Alignment.Center,
    ) { Text(label, style = style, color = PersonaTheme.palette.accent, maxLines = 1, softWrap = false) }
}

/**
 * The place of a queued message: an accent disc in glass (iOS's, `Theme.Size.badge`, growing with the text size as its
 * `scaledFrame` does, up to 1.6 times), ink on a grey disc in brief, a small square on the board.
 */
@Composable
private fun QueueNumber(number: Int) {
    val palette = PersonaTheme.palette
    val style = PersonaTheme.style
    val (fill, ink) = when (style) {
        AppStyle.Brief -> palette.surfaceStrong to palette.ink
        AppStyle.Glass, AppStyle.Board -> palette.accentSoft to palette.accent
    }
    Box(
        Modifier.size(if (style == AppStyle.Glass) Tokens.Size.badge * LocalDensity.current.fontScale.coerceIn(1f, HomeTokens.shellScaleMaximum) else 24.dp)
            .clip(if (style == AppStyle.Board) RoundedCornerShape(4.dp) else CircleShape).background(fill),
        contentAlignment = Alignment.Center,
    ) { Text("$number", style = PersonaTheme.type.micro.copy(fontWeight = FontWeight.Bold), color = ink, maxLines = 1) }
}
