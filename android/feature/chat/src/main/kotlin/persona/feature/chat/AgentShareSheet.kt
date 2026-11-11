package persona.feature.chat

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import persona.core.contracts.SessionKey
import persona.core.contracts.api.*
import persona.core.designsystem.*
import persona.core.designsystem.icons.PhIcons

/** Existing session menu entry opens a real, authority-bound management sheet. */
@Composable
fun AgentShareSheet(session: SessionKey, api: AgentShares, onDismiss: () -> Unit) {
    val scope = rememberCoroutineScope()
    val model = remember(api, session) { AgentShareModel(api, session, scope) }
    LaunchedEffect(model) { model.refreshList() }
    DisposableEffect(model) { onDispose { model.close() } }
    AgentShareContent(model, onDismiss)
}

@Composable
internal fun AgentShareContent(model: AgentShareModel, onDismiss: () -> Unit) {
    var full by remember { mutableStateOf<String?>(null) }
    var confirmRevoke by remember { mutableStateOf(false) }
    val copy = rememberCopy()
    JSheet(onDismiss, title = stringResource(R.string.chat_agent_share_title), scrollable = true) {
        Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.m)) {
            model.notice?.let { JNotice(it.text()) }
            if (model.busy) JSpinner()
            val link = model.publicLink
            if (link != null) {
                // Shown once, in memory only: closing or leaving this page drops it and nothing persists it.
                JNotice(stringResource(R.string.chat_agent_share_once), title = stringResource(R.string.chat_agent_share_created), status = Status.NeedsYou)
                JCodeBlock(link, small = true)
                // An empty notice: the sheet itself says the link was copied.
                JButton(stringResource(R.string.chat_agent_share_copy), { model.copyLink { copy(it, "") } }, Modifier.fillMaxWidth(),
                    icon = PhIcons.Copy)
                JButton(stringResource(R.string.chat_agent_share_existing), model::finishLink, Modifier.fillMaxWidth(), kind = JButtonKind.Secondary, enabled = !model.busy)
            } else when (model.page) {
                AgentSharePage.List -> {
                    JNotice(stringResource(R.string.chat_agent_share_list_note))
                    model.shares.forEach { share ->
                        JRow(pluralStringResource(R.plurals.chat_agent_share_count, share.selection.count, share.selection.count),
                            subtitle = stringResource(R.string.chat_agent_share_list_detail, shareStatus(share), shareExpiry(share.expiresAt)) + "\n" +
                                shareOptions(share), subtitleLines = Int.MAX_VALUE,
                            onClick = { model.edit(share.shareId) }, enabled = !model.busy)
                    }
                    if (model.shares.isEmpty()) Text(stringResource(R.string.chat_agent_share_empty), style = PersonaTheme.type.body, color = PersonaTheme.palette.ink2)
                    JButton(stringResource(R.string.chat_agent_share_new), model::newShare, Modifier.fillMaxWidth(), enabled = !model.busy)
                    JButton(stringResource(R.string.chat_agent_share_refresh), model::refreshList, Modifier.fillMaxWidth(), kind = JButtonKind.Secondary, enabled = !model.busy)
                }
                AgentSharePage.New, AgentSharePage.Editor -> {
                    val editing = model.page == AgentSharePage.Editor
                    val canWiden = !editing || model.detail?.canWiden == true
                    if (editing) {
                        model.detail?.share?.let { JNotice(stringResource(R.string.chat_agent_share_editor_note, shareStatus(it), shareExpiry(it.expiresAt))) }
                        if (!canWiden) JNotice(stringResource(R.string.chat_agent_share_offline))
                        JRow(stringResource(R.string.chat_agent_share_sealed), onClick = { full = "sealed" }, enabled = !model.busy)
                        if (model.removed.isNotEmpty()) JNotice(pluralStringResource(R.plurals.chat_agent_share_removed, model.removed.size, model.removed.size))
                        if (!model.replacingSelection) JButton(stringResource(R.string.chat_agent_share_replace), model::replaceSelection,
                            Modifier.fillMaxWidth(), kind = JButtonKind.Secondary, enabled = !model.busy && canWiden && model.editable)
                        else JButton(stringResource(R.string.chat_agent_share_keep), model::keepSelection, Modifier.fillMaxWidth(), kind = JButtonKind.Secondary, enabled = !model.busy)
                    }
                    if (!editing || model.replacingSelection) {
                        model.catalog?.let { catalog ->
                            if (!catalog.canShare) JNotice(failureText(catalog.reason).text(),
                                title = stringResource(R.string.chat_agent_share_unavailable_title), status = Status.NeedsYou)
                            else {
                                JNotice(pluralStringResource(R.plurals.chat_agent_share_coverage, catalog.totalMessages, catalog.totalMessages, catalog.omittedMessages))
                                if (catalog.omissions.isNotEmpty() || !catalog.complete) JNotice(
                                    (listOf(stringResource(R.string.chat_agent_share_omissions)) + catalog.omissions.distinct().map { "• " + omissionReason(it) })
                                        .joinToString("\n"), title = stringResource(R.string.chat_agent_share_omitted_title))
                            }
                        }
                        val count = model.selectedCount
                        JRow(stringResource(R.string.chat_agent_share_selection), subtitle = selectionLabel(model.selection) +
                            (count?.let { " · " + stringResource(R.string.chat_agent_share_selected_count, it) } ?: ""),
                            onClick = { full = "selection" }, enabled = !model.busy && model.catalog?.canShare == true)
                        JButton(stringResource(R.string.chat_agent_share_reload_catalog), model::loadCatalog, Modifier.fillMaxWidth(),
                            kind = JButtonKind.Quiet, enabled = !model.busy)
                    }
                    val policyEnabled = !model.busy && (!editing || model.editable)
                    JSwitchRow(stringResource(R.string.chat_agent_share_future), model.includeFuture, model::future,
                        subtitle = stringResource(R.string.chat_agent_share_future_note), subtitleLines = Int.MAX_VALUE,
                        enabled = policyEnabled && (model.includeFuture || (canWiden && (editing || model.catalog?.canIncludeFuture == true))))
                    if (model.canResumeFuture) JButton(stringResource(R.string.chat_agent_share_resume_future), model::resumeFuture,
                        Modifier.fillMaxWidth(), kind = JButtonKind.Secondary, enabled = policyEnabled && !model.resumingFuture)
                    JSwitchRow(stringResource(R.string.chat_agent_share_tools), model.showTools, model::tools,
                        subtitle = stringResource(R.string.chat_agent_share_tools_note), subtitleLines = Int.MAX_VALUE,
                        enabled = policyEnabled && (model.showTools || canWiden))
                    Text(stringResource(R.string.chat_agent_share_lifetime), style = PersonaTheme.type.label, color = PersonaTheme.palette.ink2)
                    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                        listOf(1, 3, 7, 30).forEach { days ->
                            JButton(stringResource(R.string.chat_agent_share_days, days), { model.lifetime(days) }, Modifier.weight(1f),
                                size = JButtonSize.Small, kind = if (model.days == days && (!editing || model.expiry != model.detail?.share?.expiresAt)) JButtonKind.Primary else JButtonKind.Secondary,
                                enabled = policyEnabled && (canWiden || model.detail?.share?.let { !java.time.Instant.parse(it.createdAt).plusSeconds(days.toLong() * 86400).isAfter(java.time.Instant.parse(it.expiresAt)) } == true))
                        }
                    }
                    JNotice(stringResource(R.string.chat_agent_share_cap))
                    if (model.needsPreview) {
                        JButton(stringResource(R.string.chat_agent_share_prepare), model::preparePreview, Modifier.fillMaxWidth(),
                            kind = JButtonKind.Secondary, enabled = policyEnabled && canWiden && model.validSelection &&
                                (editing && !model.replacingSelection || model.catalog?.canShare == true && model.catalog?.complete == true))
                        model.preview?.let { preview ->
                            JRow(stringResource(R.string.chat_agent_share_preview), subtitle = stringResource(R.string.chat_agent_share_preview_count, preview.selectedCount, preview.omittedCount),
                                onClick = { full = "preview" }, enabled = !model.busy)
                            if (preview.requiresInsecureTransportAck) {
                                // The hub's localized plain text; a missing or oversized one falls back to ours.
                                JNotice(preview.transportWarning?.takeIf { it.isNotBlank() && it.length <= 1000 }
                                    ?: stringResource(R.string.chat_agent_share_transport_warning), status = Status.NeedsYou, icon = PhIcons.Warning)
                                JSwitchRow(stringResource(R.string.chat_agent_share_transport_ack), model.transportAcknowledged, model::acknowledgeTransport, enabled = !model.busy)
                            }
                            JSwitchRow(stringResource(R.string.chat_agent_share_ack), model.acknowledged, model::acknowledge,
                                subtitle = stringResource(R.string.chat_agent_share_sensitive), subtitleLines = Int.MAX_VALUE, enabled = !model.busy)
                        }
                    }
                    if (editing) {
                        JButton(stringResource(R.string.chat_agent_share_save), model::save, Modifier.fillMaxWidth(),
                            enabled = policyEnabled && model.changed && (!model.needsPreview || model.confirmed))
                        JButton(stringResource(R.string.chat_agent_share_revoke), { confirmRevoke = true }, Modifier.fillMaxWidth(),
                            kind = JButtonKind.Destructive, enabled = !model.busy && model.editable)
                    } else JButton(stringResource(R.string.chat_agent_share_create), model::create, Modifier.fillMaxWidth(),
                        enabled = !model.busy && model.confirmed && !model.uncertainCreation)
                    JButton(stringResource(R.string.chat_agent_share_existing), model::refreshList, Modifier.fillMaxWidth(), kind = JButtonKind.Quiet, enabled = !model.busy)
                }
            }
        }
    }
    if (confirmRevoke) JDialog(onDismiss = { confirmRevoke = false }, title = stringResource(R.string.chat_agent_share_revoke),
        text = stringResource(R.string.chat_agent_share_revoke_warning), destructive = true,
        confirm = stringResource(R.string.chat_agent_share_revoke) to { confirmRevoke = false; model.revoke() })
    if (full != null) JFullScreen(onDismiss = { full = null }) {
        JPage(title = stringResource(when (full) { "selection" -> R.string.chat_agent_share_selection; "sealed" -> R.string.chat_agent_share_sealed; else -> R.string.chat_agent_share_preview }),
            onBack = { full = null }) { padding ->
            // The same side margin the sheet gives its rows, so Brief's edge-to-edge rows do not touch the screen edge.
            LazyColumn(Modifier.fillMaxSize().padding(horizontal = Tokens.Space.l), contentPadding = padding, verticalArrangement = Arrangement.spacedBy(Tokens.Space.m)) {
                item { model.notice?.let { JNotice(it.text()) } }
                when (full) {
                    "selection" -> {
                        item {
                            Column {
                                listOf("all", "from", "individual").forEach { mode ->
                                    JRow(stringResource(when (mode) { "all" -> R.string.chat_agent_share_all; "from" -> R.string.chat_agent_share_from; else -> R.string.chat_agent_share_individual }),
                                        onClick = { model.mode(mode) }, enabled = !model.busy, chevron = false,
                                        trailing = { JRadio(mode == when (model.selection) { ShareSelection.All -> "all"; is ShareSelection.From -> "from"; else -> "individual" }, null) })
                                }
                                JNotice(stringResource(R.string.chat_agent_share_all_note))
                            }
                        }
                        items(model.catalogMessages, key = { it.messageId }) { message ->
                            val selected = when (val selection = model.selection) {
                                ShareSelection.All -> message.selectable
                                is ShareSelection.From -> selection.fromMessageId == message.messageId
                                is ShareSelection.Individual -> message.messageId in selection.messageIds
                            }
                            JRow(roleLabel(message.role), subtitle = message.text, subtitleLines = Int.MAX_VALUE, chevron = false,
                                onClick = { model.select(message.messageId) }, enabled = !model.busy && message.selectable && model.selection != ShareSelection.All,
                                trailing = { if (model.selection is ShareSelection.From) JRadio(selected, null, enabled = message.selectable)
                                    else JCheckbox(selected, null, enabled = message.selectable) })
                        }
                        if (model.catalog?.nextCursor != null) item { JButton(stringResource(R.string.chat_agent_share_more), model::moreCatalog, enabled = !model.busy, loading = model.busy) }
                    }
                    "sealed" -> {
                        item { JNotice(stringResource(R.string.chat_agent_share_remove_note)) }
                        items(model.sealedMessages, key = { it.messageId }) { message ->
                            JRow(roleLabel(message.role), subtitle = message.text, subtitleLines = Int.MAX_VALUE, chevron = false,
                                onClick = { model.remove(message.messageId) }, enabled = !model.busy && model.editable && !model.replacingSelection,
                                trailing = { JCheckbox(message.messageId in model.removed, null) })
                        }
                        if (model.sealedCursor != null) item { JButton(stringResource(R.string.chat_agent_share_more), model::moreSealed, enabled = !model.busy, loading = model.busy) }
                    }
                    else -> {
                        item { JNotice(stringResource(R.string.chat_agent_share_sensitive)) }
                        items(model.previewMessages, key = { it.messageId }) { message -> SharePreviewMessage(message) }
                    }
                }
                item { JButton(stringResource(R.string.chat_agent_share_done), { full = null }, Modifier.fillMaxWidth()) }
            }
        }
    }
}

@Composable private fun SharePreviewMessage(message: ShareMessage) {
    var expanded by remember(message.messageId) { mutableStateOf(false) }
    JCard(padding = Tokens.Space.m) {
        Text(roleLabel(message.role), style = PersonaTheme.type.label, color = PersonaTheme.palette.ink2)
        Text(message.text, style = PersonaTheme.type.body, color = PersonaTheme.palette.ink)
        if (!message.execution.isNullOrEmpty()) {
            JRow(stringResource(R.string.chat_agent_share_work), onClick = { expanded = !expanded })
            if (expanded) message.execution.orEmpty().forEach { execution ->
                Text(when (execution) {
                    is ShareExecution.Text -> execution.text
                    is ShareExecution.Agent -> execution.text
                    is ShareExecution.Tool -> listOf(execution.name, execution.arguments, execution.results.joinToString("\n") { it.text }).joinToString("\n")
                }, style = PersonaTheme.type.body, color = PersonaTheme.palette.ink2)
            }
        }
    }
}
@Composable private fun shareExpiry(value: String): String {
    val locale = currentLocale
    return LocalizedFormats.format(java.time.Instant.parse(value), LocalizedFormats.DATE_TIME, locale)
}
@Composable private fun shareOptions(share: ShareMetadata) = stringResource(R.string.chat_agent_share_options,
    stringResource(if (share.showTools) R.string.chat_agent_share_tools_on else R.string.chat_agent_share_tools_off),
    stringResource(if (publishesFutureMessages(share)) R.string.chat_agent_share_future_on else R.string.chat_agent_share_future_off))
/** The list describes actual publication, not the retained opt-in preference in the editor. */
internal fun publishesFutureMessages(share: ShareMetadata) = share.status == "active" &&
    share.publicationStatus == "live" && share.includeFuture
@Composable private fun omissionReason(code: String) = stringResource(when (code) {
    "unknown_provenance" -> R.string.chat_agent_share_omission_unknown_provenance
    "unfinished_turn" -> R.string.chat_agent_share_omission_unfinished_turn
    "unassociated_execution" -> R.string.chat_agent_share_omission_unassociated_execution
    "component_incomplete" -> R.string.chat_agent_share_omission_component_incomplete
    "component_limit" -> R.string.chat_agent_share_omission_component_limit
    else -> R.string.chat_agent_share_omission_other
})
@Composable private fun roleLabel(role: String) = stringResource(if (role == "user") R.string.chat_agent_share_user else R.string.chat_agent_share_assistant)
@Composable private fun selectionLabel(selection: ShareSelection) = stringResource(when (selection) {
    ShareSelection.All -> R.string.chat_agent_share_all; is ShareSelection.From -> R.string.chat_agent_share_from; is ShareSelection.Individual -> R.string.chat_agent_share_individual
})
@Composable private fun shareStatus(share: ShareMetadata) = stringResource(when {
    share.status == "revoked" -> R.string.chat_agent_share_status_revoked
    share.status == "expired" -> R.string.chat_agent_share_status_expired
    share.publicationStatus == "paused" -> R.string.chat_agent_share_status_paused
    share.publicationStatus == "live" -> R.string.chat_agent_share_status_live
    else -> R.string.chat_agent_share_status_frozen
})
