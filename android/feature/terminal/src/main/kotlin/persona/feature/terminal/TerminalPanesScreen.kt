package persona.feature.terminal

import androidx.compose.foundation.layout.*
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.repeatOnLifecycle
import persona.core.contracts.AgentState
import persona.core.contracts.api.isPaneAddress
import persona.core.designsystem.*
import persona.core.designsystem.icons.PhIcons
import persona.core.navigation.Navigator
import persona.core.navigation.TerminalRoute

/**
 * "Terminal" (docs/integrations/terminal.md "Standalone terminal"): machine -> workspace -> tab -> pane, agents and plain shells, each opened in the
 * terminal page; "New tab" opens a shell tab in a workspace. A pane with an agent opens by its session (the stream its
 * chat's terminal uses), the others by their address.
 */
@Composable
fun TerminalPanesScreen(model: TerminalPanesViewModel, navigator: Navigator) {
    val state by model.state.collectAsStateWithLifecycle()
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    LaunchedEffect(model, lifecycle) { lifecycle.repeatOnLifecycle(Lifecycle.State.RESUMED) { model.poll() } }
    val feedback = LocalAppFeedback.current
    val resources = LocalResources.current
    LaunchedEffect(model, feedback, resources, navigator) {
        model.events.collect { event ->
            when (event) {
                is TerminalPanesEvent.Open -> navigator.navigate(TerminalRoute(event.machineId, event.terminal, event.title))
                is TerminalPanesEvent.NewTabFailed -> feedback?.notice(resources.getString(R.string.terminal_panes_new_tab_failed, event.error))
            }
        }
    }
    val untitled = stringResource(R.string.terminal_panes_untitled)
    JListPage(
        title = stringResource(R.string.terminal_panes_title), summary = stringResource(R.string.terminal_panes_summary),
        onBack = navigator::back, refreshing = state.busy, onRefresh = model::refresh,
        actions = { JIconButton(PhIcons.ArrowClockwise, stringResource(R.string.terminal_panes_refresh), model::refresh, enabled = !state.busy) },
    ) {
        if (state.machines.isEmpty()) item("empty") {
            JEmptyState(stringResource(R.string.terminal_panes_no_computers), stringResource(R.string.terminal_panes_no_computers_detail),
                icon = PhIcons.TerminalWindow)
        }
        for (entry in state.machines) {
            val machine = entry.machine
            item("machine-${machine.id}", contentType = "machine") {
                JSectionHeader(machine.displayName, trailing = if (machine.online) null else stringResource(R.string.terminal_panes_offline))
            }
            if (!machine.online) continue
            val listing = entry.listing
            when {
                listing == null -> item("loading-${machine.id}", contentType = "note") { Note(stringResource(R.string.terminal_panes_loading)) }
                listing.error != null && listing.response == null -> item("error-${machine.id}", contentType = "error") {
                    JNotice(listing.error, status = Status.NeedsYou, actionLabel = stringResource(R.string.terminal_retry), onAction = model::refresh)
                }
                entry.spaces.isEmpty() -> item("none-${machine.id}", contentType = "note") { Note(stringResource(R.string.terminal_panes_no_panes)) }
            }
            for (space in entry.spaces) {
                item(spaceItemKey(machine.id, space), contentType = "space") {
                    SpaceHeader(space, opening = state.opening != null, thisOne = state.opening == openingKey(machine.id, space)) {
                        model.newTab(machine.id, space)
                    }
                }
                for (tab in space.tabs) {
                    // A tab named like its workspace, the only one there, needs no heading of its own.
                    if (space.tabs.size > 1 || tab.label != space.label) item(tabItemKey(machine.id, space, tab), contentType = "tab") {
                        Note(stringResource(R.string.terminal_panes_tab, tab.label))
                    }
                    tab.panes.forEachIndexed { index, pane ->
                        item(paneItemKey(machine.id, space, pane), contentType = "pane") {
                            JGroupItem(GroupPosition.of(index, tab.panes.size)) {
                                PaneRow(pane, untitled) {
                                    // A session's terminal is titled by the session; a pane by its place.
                                    navigator.navigate(TerminalRoute(pane.key.machineId, pane.key.sessionId,
                                        if (pane.key.isPaneAddress) paneTrail(space, tab, pane, untitled) else ""))
                                }
                            }
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun SpaceHeader(space: TermSpace, opening: Boolean, thisOne: Boolean, onNewTab: () -> Unit) {
    Row(Modifier.fillMaxWidth().padding(top = Tokens.Space.m, bottom = Tokens.Space.xs, start = Tokens.Space.xs),
        verticalAlignment = Alignment.CenterVertically) {
        Column(Modifier.weight(1f)) {
            Text(space.label, style = PersonaTheme.type.body, color = PersonaTheme.palette.ink, maxLines = 1, overflow = TextOverflow.Ellipsis)
            if (space.branch.isNotEmpty()) Text(space.branch, style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink3, maxLines = 1,
                overflow = TextOverflow.Ellipsis)
        }
        if (space.canOpen) {
            if (thisOne) JSpinner(Modifier.size(Tokens.Space.Standard.l))
            else JTextButton(stringResource(R.string.terminal_panes_new_tab), onNewTab, enabled = !opening, icon = PhIcons.Plus)
        }
    }
}

@Composable
private fun PaneRow(pane: TermPane, untitled: String, onOpen: () -> Unit) {
    val status = pane.state?.let(::statusOf)
    val subtitle = listOfNotNull(status?.localizedLabel, pane.detail.ifEmpty { null }).joinToString(" · ")
    JRow(pane.title.ifEmpty { untitled }, subtitle = subtitle.ifEmpty { null }, onClick = onOpen,
        leading = {
            if (status != null) JStatusDot(status)
            else JIcon(PhIcons.TerminalWindow, null, size = Tokens.Space.l, tint = PersonaTheme.palette.ink2, decorative = true)
        })
}

@Composable
private fun Note(text: String) {
    Text(text, Modifier.padding(horizontal = Tokens.Space.xs, vertical = Tokens.Space.s), style = PersonaTheme.type.caption,
        color = PersonaTheme.palette.ink3)
}

private fun statusOf(state: AgentState) = when (state) {
    AgentState.NeedsYou -> Status.NeedsYou; AgentState.Working -> Status.Working
    AgentState.Unread -> Status.Unread; AgentState.Idle -> Status.Idle
    AgentState.Offline -> Status.Offline; AgentState.Unknown -> Status.Unknown
}
