package persona.feature.settings

import android.content.res.Resources
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.res.stringResource
import androidx.compose.foundation.gestures.awaitEachGesture
import androidx.compose.foundation.gestures.awaitFirstDown
import androidx.compose.foundation.gestures.waitForUpOrCancellation
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyItemScope
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.graphics.drawOutline
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.input.pointer.PointerEventPass
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.semantics.CustomAccessibilityAction
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.customActions
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardCapitalization
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.zIndex
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import persona.core.contracts.api.*
import persona.core.designsystem.*
import persona.core.designsystem.icons.PhIcons
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch

/** One frame of the editable candidate list: the editor, what the connection reports, the rows shown and the drag. */
internal class EndpointsSnapshot(
    val editor: EndpointEditorState,
    val routes: ConnectionEndpointsState,
    val ui: EndpointEditorUi,
    val rows: List<EndpointRow>,
    val drag: EndpointDragState,
    /** The page's resources, for text built outside composition. */
    val resources: Resources,
)

/** Null where the connection cannot change its routes (demo mode); "Connection diagnostics" then stays read-only as before. */
@Composable
internal fun rememberEndpointsSnapshot(endpoints: ConnectionEndpoints?, probes: List<ConnectionCandidate>, list: LazyListState): EndpointsSnapshot? {
    endpoints ?: return null
    val editor = remember(endpoints) { EndpointEditorState(endpoints) }
    val routes by editor.routes.collectAsStateWithLifecycle()
    val ui by editor.ui.collectAsStateWithLifecycle()
    val shown = remember(routes.entries, ui.drag, ui.pending) { shownEntries(routes.entries, ui) }
    val resources = LocalResources.current
    val rows = remember(routes, probes, ui.testing, shown, resources) { endpointRows(resources, routes, probes, ui.testing, shown) }
    val drag = rememberEndpointDrag(list, editor)
    return EndpointsSnapshot(editor, routes, ui, rows, drag, resources)
}

/** "Current connection" with the url in use; replaces the read-only "Current path" row. */
internal fun LazyListScope.editableCurrentConnection(snapshot: EndpointsSnapshot, data: ConnectionDiagnostics, connection: ConnectionState) {
    val active = activeRoute(snapshot.resources, snapshot.routes, data.currentPath, connection)
    item { JSectionHeader(stringResource(R.string.settings_endpoint_current_header)) }
    item {
        JGroupItem(GroupPosition.of(0, 3)) {
            SelectionContainer { JRow(stringResource(R.string.settings_endpoint_in_use), subtitle = active.url, value = active.label, subtitleLines = Int.MAX_VALUE) }
        }
    }
    item { JGroupItem(GroupPosition.of(1, 3)) { DiagnosticValue(stringResource(R.string.settings_endpoint_last_received), localTime(data.lastReceivedAt, currentLocale)) } }
    item { JGroupItem(GroupPosition.of(2, 3)) { DiagnosticValue(stringResource(R.string.settings_endpoint_last_disconnect), data.lastDisconnectReason) } }
}

/**
 * "Candidate addresses": one row per candidate in the order the connection tries them, top first, disabled ones in their place.
 * Dragging a row (its handle, or a long press anywhere on it) or "Move up" / "Move down" in its actions changes that order.
 */
internal fun LazyListScope.editableCandidates(snapshot: EndpointsSnapshot, scope: CoroutineScope) {
    val editor = snapshot.editor
    val drag = snapshot.drag
    // Adding is the group's last row rather than a header action, which the section header cannot keep flush right.
    item(key = "endpoints-header") { JSectionHeader(stringResource(R.string.settings_endpoint_candidates), count = snapshot.rows.size) }
    if (snapshot.rows.isNotEmpty()) item(key = "endpoints-rule") { SettingsCaption(stringResource(R.string.settings_endpoint_rule_note), Modifier.padding(bottom = Tokens.Space.m)) }
    snapshot.ui.message?.let { message ->
        item(key = "endpoints-message") { JNotice(message.text(), Modifier.padding(bottom = Tokens.Space.m), status = Status.NeedsYou) }
    }
    if (snapshot.rows.isEmpty()) item(key = "endpoints-empty") { JEmptyState(stringResource(R.string.settings_endpoint_empty_title), stringResource(R.string.settings_endpoint_empty_body)) }
    val count = snapshot.rows.size + 1
    itemsIndexed(snapshot.rows, key = { _, row -> row.url }, contentType = { _, _ -> "endpoint" }) { index, row ->
        CandidateItem(row, GroupPosition.of(index, count), drag) {
            EndpointRowView(
                row, drag,
                onOpen = { editor.openActions(row.url) },
                onTest = { scope.launch { editor.test(row.url) } },
                onMoveUp = { scope.launch { editor.moveUp(row.url) } },
                onMoveDown = { scope.launch { editor.moveDown(row.url) } },
            )
        }
    }
    item(key = "endpoints-add") {
        JGroupItem(GroupPosition.of(count - 1, count), Modifier.animateItem(placementSpec = placement())) {
            JRow(stringResource(R.string.settings_endpoint_add), leadingIcon = PhIcons.Plus, subtitle = stringResource(R.string.settings_endpoint_add_subtitle), onClick = editor::startAdding, chevron = false)
        }
    }
    item(key = "endpoints-notes") {
        Column(Modifier.animateItem(placementSpec = placement()).padding(top = Tokens.Space.m), verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
            val custom = snapshot.routes.customOrder
            JButton(stringResource(R.string.settings_endpoint_reset), editor::askResetOrder, kind = JButtonKind.Secondary, size = JButtonSize.Small,
                icon = PhIcons.ArrowClockwise, enabled = custom)
            SettingsCaption(stringResource(R.string.settings_endpoint_order_note, stringResource(R.string.settings_endpoint_default_rule)))
            SettingsCaption(stringResource(R.string.settings_endpoint_test_note))
        }
    }
}

/** Rows make way with the style's motion; none when motion is reduced. */
@Composable
private fun placement() = if (PersonaTheme.reduceMotion) null else PersonaTheme.motion.enter<IntOffset>()

/**
 * One candidate in the group. Picked up, it leaves the group as a card of its own (clipped to the card shape, an
 * opaque backing under the glass, a shadow) and is drawn above the others, which make way; at rest it is a piece of
 * the group again. Only the group position changes, never `separate`: the board style draws separate items through
 * another call site, so flipping it would rebuild the row and cancel the drag under the finger.
 */
@Composable
private fun LazyItemScope.CandidateItem(row: EndpointRow, position: GroupPosition, drag: EndpointDragState, content: @Composable () -> Unit) {
    val lifted = drag.lifted(row.url)
    val shape = PersonaTheme.shapes.cardShape
    val backing = PersonaTheme.palette.surfaceOpaque
    val elevation = with(LocalDensity.current) { LiftElevation.toPx() }
    val url = row.url
    val modifier = if (lifted) {
        Modifier.zIndex(1f)
            .graphicsLayer {
                val lift = drag.liftOf(url)
                translationY = drag.translation(url)
                shadowElevation = elevation * lift
                scaleX = 1f + LiftScale * lift
                scaleY = scaleX
                this.shape = shape
                clip = true
            }
            .drawBehind { drawOutline(shape.createOutline(size, layoutDirection, this), backing.copy(alpha = backing.alpha * drag.liftOf(url))) }
    } else Modifier.animateItem(placementSpec = placement())
    Box(modifier) {
        JGroupItem(if (lifted) GroupPosition.Single else position) { content() }
    }
}

/**
 * The url must wrap (an IPv6 url is never cut off), so the row's middle is [JRow]'s slot: its padding, height and click
 * line up with the rows around it in every style. A long press drags instead of opening the actions: it is detected
 * inside the slot, which sees a down before the row's click does; a right click still opens the actions.
 */
@Composable
private fun EndpointRowView(
    row: EndpointRow, drag: EndpointDragState,
    onOpen: () -> Unit, onTest: () -> Unit, onMoveUp: () -> Unit, onMoveDown: () -> Unit,
) {
    val palette = PersonaTheme.palette
    val moves = moveActions(row, onMoveUp, onMoveDown)
    val turn = turn(row)
    JRow(
        modifier = Modifier.onSecondaryClick(onOpen).semantics {
            customActions = moves
            stateDescription = turn
        },
        onClick = onOpen, onLongClick = null, onClickLabel = stringResource(R.string.settings_endpoint_more),
        trailing = {
            // While a test runs the button is disabled; a tap on it must not fall through to the row and open its actions.
            Box(Modifier.pointerInput(row.testing) {
                if (row.testing) awaitEachGesture {
                    awaitFirstDown(pass = PointerEventPass.Initial).consume()
                    waitForUpOrCancellation(PointerEventPass.Initial)?.consume()
                }
            }) {
                // The icon keeps the width steady when the spinner takes its place.
                JButton(stringResource(R.string.settings_endpoint_test), onTest, kind = JButtonKind.Secondary, size = JButtonSize.Small, icon = PhIcons.Gauge, loading = row.testing)
            }
        },
    ) {
        Row(Modifier.weight(1f).dragAfterLongPress(drag, row.url), verticalAlignment = Alignment.CenterVertically) {
            DragHandle(row, drag, moves)
            Row(Modifier.weight(1f).stableAlpha(if (row.entry.enabled) 1f else DimmedAlpha)) {
                JStatusDot(row.status, Modifier.padding(top = Tokens.Space.s))
                Spacer(Modifier.width(Tokens.Space.m))
                Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.xxs)) {
                    Text(row.url, style = PersonaTheme.type.body, color = palette.ink)
                    Text(row.origin, style = PersonaTheme.type.caption, color = palette.ink2)
                    if (row.tags.isNotEmpty()) FlowRow(
                        Modifier.padding(vertical = Tokens.Space.xxs),
                        horizontalArrangement = Arrangement.spacedBy(Tokens.Space.xs),
                        verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs),
                    ) { row.tags.forEach { JTag(stringResource(it.label), status = it.status) } }
                    if (!row.entry.enabled) Text(stringResource(R.string.settings_endpoint_skipped_note), style = PersonaTheme.type.caption, color = palette.ink2)
                    row.outcome?.let {
                        Text(it.text, style = PersonaTheme.type.caption, color = if (it.failed) palette.needsYou.text else palette.ink2)
                        Text(it.label, style = PersonaTheme.type.caption, color = palette.ink3)
                    }
                }
            }
        }
    }
}

/**
 * The leading handle: the row's turn in the order tried (none for a disabled row, which is skipped) over a grip.
 * Touching it picks the row up at once; it is its own accessibility element, with "Move up" / "Move down" as actions.
 */
@Composable
private fun DragHandle(row: EndpointRow, drag: EndpointDragState, moves: List<CustomAccessibilityAction>) {
    val palette = PersonaTheme.palette
    val description = stringResource(R.string.settings_endpoint_drag)
    Column(
        Modifier.width(HandleWidth).heightIn(min = Tokens.MinTouch.android)
            .dragHandle(drag, row.url)
            .semantics(mergeDescendants = true) {
                contentDescription = description
                customActions = moves
            },
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(Tokens.Space.xxs, Alignment.CenterVertically),
    ) {
        row.rank?.let {
            Text("$it", style = PersonaTheme.type.micro, color = palette.ink2, maxLines = 1, softWrap = false,
                modifier = Modifier.clearAndSetSemantics {})
        }
        Grip(if (drag.dragging == row.url) palette.ink2 else palette.ink3)
    }
}

@Composable
private fun moveActions(row: EndpointRow, onMoveUp: () -> Unit, onMoveDown: () -> Unit): List<CustomAccessibilityAction> = listOfNotNull(
    CustomAccessibilityAction(stringResource(R.string.settings_endpoint_move_up)) { onMoveUp(); true }.takeIf { row.canMoveUp },
    CustomAccessibilityAction(stringResource(R.string.settings_endpoint_move_down)) { onMoveDown(); true }.takeIf { row.canMoveDown },
)

/** "Tried 2nd", or that a disabled row is skipped. */
@Composable
private fun turn(row: EndpointRow): String =
    row.rank?.let { stringResource(R.string.settings_endpoint_turn, it) } ?: stringResource(R.string.settings_endpoint_skipped)

/** The sheets and the dialogs the candidate list opens. */
@Composable
internal fun EndpointOverlays(snapshot: EndpointsSnapshot) {
    val scope = rememberCoroutineScope()
    val copy = rememberCopy()
    val editor = snapshot.editor
    val ui = snapshot.ui
    ui.actions?.let { url ->
        val row = snapshot.rows.firstOrNull { it.url == url }
        if (row == null) LaunchedEffect(url) { editor.closeActions() }
        else EndpointActions(
            row, busy = ui.busy, onDismiss = editor::closeActions,
            onTest = { scope.launch { editor.test(url) } },
            onMoveUp = { scope.launch { editor.moveUp(url) } },
            onMoveDown = { scope.launch { editor.moveDown(url) } },
            onToggle = { scope.launch { editor.setEnabled(url, !row.entry.enabled) } },
            onCopy = { copy(url, null) },
            onEdit = { editor.startEditing(url) },
            onDelete = { editor.askDelete(url) },
        )
    }
    ui.sheet?.let { sheet ->
        AddressSheet(sheet, pinned = snapshot.routes.pinned, onChange = editor::updateAddress,
            onSave = { scope.launch { editor.save() } }, onDismiss = editor::dismissSheet)
    }
    ui.deleting?.let { url ->
        val delete: () -> Unit = { scope.launch { editor.confirmDelete() } }
        JDialog(
            onDismiss = editor::cancelDelete, title = stringResource(R.string.settings_endpoint_delete_title), text = stringResource(R.string.settings_endpoint_delete_body, url),
            confirm = stringResource(R.string.settings_endpoint_delete) to delete, destructive = true, confirmEnabled = !ui.busy,
        )
    }
    if (ui.resetting) {
        val reset: () -> Unit = { scope.launch { editor.confirmResetOrder() } }
        JDialog(onDismiss = editor::cancelResetOrder, title = stringResource(R.string.settings_endpoint_reset_title),
            text = stringResource(R.string.settings_endpoint_reset_note, stringResource(R.string.settings_endpoint_default_rule)),
            confirm = stringResource(R.string.settings_endpoint_reset_confirm) to reset)
    }
}

@Composable
private fun EndpointActions(
    row: EndpointRow, busy: Boolean, onDismiss: () -> Unit, onTest: () -> Unit, onMoveUp: () -> Unit, onMoveDown: () -> Unit,
    onToggle: () -> Unit, onCopy: () -> Unit, onEdit: () -> Unit, onDelete: () -> Unit,
) {
    val entry = row.entry
    val manual = entry.source == EndpointSource.Manual
    val turn = turn(row)
    JActionSheet(
        actions = buildList {
            add(JAction(stringResource(R.string.settings_endpoint_test), PhIcons.Gauge, enabled = !row.testing, onClick = onTest))
            add(JAction(stringResource(R.string.settings_endpoint_move_up), PhIcons.ArrowUp, enabled = row.canMoveUp, onClick = onMoveUp))
            add(JAction(stringResource(R.string.settings_endpoint_move_down), PhIcons.ArrowDown, enabled = row.canMoveDown, onClick = onMoveDown))
            add(JAction(stringResource(if (entry.enabled) R.string.settings_endpoint_disable else R.string.settings_endpoint_enable), if (entry.enabled) PhIcons.Prohibit else PhIcons.CheckCircle, enabled = !busy, onClick = onToggle))
            add(JAction(stringResource(R.string.settings_endpoint_copy), PhIcons.Copy, onClick = onCopy))
            if (manual) {
                add(JAction(stringResource(R.string.settings_endpoint_edit), PhIcons.PencilSimple, enabled = !busy, separatorBefore = true, onClick = onEdit))
                add(JAction(stringResource(R.string.settings_endpoint_delete), PhIcons.Trash, destructive = true, enabled = !busy, onClick = onDelete))
            }
        },
        onDismiss = onDismiss, title = entry.url,
        note = stringResource(when {
            manual -> R.string.settings_endpoint_note_manual
            entry.enabled -> R.string.settings_endpoint_note_fixed
            else -> R.string.settings_endpoint_note_fixed_disabled
        }, turn, row.origin),
    )
}

@Composable
private fun AddressSheet(sheet: EndpointSheet, pinned: Boolean, onChange: (String) -> Unit, onSave: () -> Unit, onDismiss: () -> Unit) {
    JSheet(onDismiss, title = stringResource(if (sheet.editing == null) R.string.settings_endpoint_add else R.string.settings_endpoint_edit_title), dismissible = !sheet.saving, scrollable = true) {
        Column(Modifier.fillMaxWidth().imePadding(), verticalArrangement = Arrangement.spacedBy(Tokens.Space.m)) {
            JTextField(
                sheet.address, onChange, Modifier.fillMaxWidth(), placeholder = "https://hub.example.com:18662", label = stringResource(R.string.settings_endpoint_address),
                supportingText = sheet.hint(LocalResources.current), singleLine = true, enabled = !sheet.saving, isError = sheet.check is EndpointCheck.Invalid,
                keyboardOptions = KeyboardOptions(
                    capitalization = KeyboardCapitalization.None, autoCorrectEnabled = false,
                    keyboardType = KeyboardType.Uri, imeAction = ImeAction.Done,
                ),
                keyboardActions = KeyboardActions(onDone = { if (sheet.canSave) onSave() }),
            )
            SettingsCaption(stringResource(R.string.settings_endpoint_address_forms))
            if (!pinned) SettingsCaption(stringResource(R.string.settings_endpoint_unpinned_note))
            sheet.error?.let { JNotice(it.text(), status = Status.NeedsYou) }
            Row(horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                JButton(stringResource(R.string.settings_endpoint_cancel), onDismiss, Modifier.weight(1f), kind = JButtonKind.Quiet, enabled = !sheet.saving)
                JButton(stringResource(R.string.settings_endpoint_save), onSave, Modifier.weight(1f), enabled = sheet.canSave, loading = sheet.saving, loadingText = stringResource(R.string.settings_endpoint_saving))
            }
        }
    }
}

/** The handle: the grip (with the row's turn over it) and the gap before the status dot; JRow pads the row itself. */
private val HandleWidth = GripWidth + Tokens.Space.Standard.m

/** A picked-up row casts a shadow and grows slightly, so it reads as lifted off the list. */
private val LiftElevation = Tokens.Space.Standard.s
private const val LiftScale = 0.02f

/** Disabled candidates read as switched off, like a disabled row. */
private const val DimmedAlpha = 0.45f
