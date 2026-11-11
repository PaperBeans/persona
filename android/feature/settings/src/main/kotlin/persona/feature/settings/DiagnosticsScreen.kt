package persona.feature.settings

import persona.core.designsystem.resolve
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LifecycleEventEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import persona.core.contracts.api.*
import persona.core.designsystem.*
import kotlinx.coroutines.launch
import java.time.Instant
import java.time.LocalDate
import java.time.OffsetDateTime
import java.time.ZoneId
import android.content.res.Resources
import androidx.compose.ui.res.stringResource

@Composable
internal fun DiagnosticsScreen(services: SettingsServices, onBack: (() -> Unit)? = null, footer: @Composable () -> Unit = {}) {
    val context = LocalContext.current
    val resources = context.resources
    val scope = rememberCoroutineScope()
    val copy = rememberCopy()
    var background by remember { mutableStateOf(BackgroundDiagnostics.read(context)) }
    var backgroundCopied by remember { mutableStateOf(false) }
    val controller = remember(services) {
        DiagnosticsState(services.connection, services.liveDiagnostics, services.diagnostics, services.pairingAttempts)
    }
    val data by controller.data.collectAsStateWithLifecycle()
    val pairing by controller.pairing.collectAsStateWithLifecycle()
    val busy by controller.busy.collectAsStateWithLifecycle()
    val message by controller.message.collectAsStateWithLifecycle()
    val summary by controller.summary.collectAsStateWithLifecycle()
    val state by services.connection.state.collectAsStateWithLifecycle()
    var network by remember { mutableStateOf(PhoneReadiness.network(context)) }
    var fingerprintExpanded by rememberSaveable { mutableStateOf(false) }
    val list = rememberLazyListState()
    val endpoints = rememberEndpointsSnapshot(services.endpoints, data.candidates, list)
    LifecycleEventEffect(Lifecycle.Event.ON_RESUME) {
        network = PhoneReadiness.network(context)
        background = BackgroundDiagnostics.read(context)
        backgroundCopied = false
        scope.launch { controller.refresh() }
        scope.launch { controller.refreshSummary() }
    }
        val summaryText = summary?.let { disconnectSummaryText(it) }
    JListPage(title = stringResource(R.string.settings_diag_title), onBack = onBack, state = list) {
        item { ConnectionStatus(state) }
        // A failed pairing comes first: before a pairing works there is no connection to show anything about.
        pairing?.takeIf { it.failed }?.let { pairingAttempt(it, resources) }
        if (endpoints != null) editableCurrentConnection(endpoints, data, state)
        else diagnosticGroup(resources.getString(R.string.settings_diag_current), listOf(
            resources.getString(R.string.settings_diag_current_path) to data.currentPath?.let { path ->
                val parts = path.split(" · ", limit = 2)
                listOf(routeKind(parts.first(), resources)).plus(parts.drop(1)).joinToString(resources.getString(R.string.settings_list_separator))
            },
            resources.getString(R.string.settings_diag_last_received) to localTime(data.lastReceivedAt, resources.textLocale),
            resources.getString(R.string.settings_diag_last_disconnect) to data.lastDisconnectReason,
        ))
        if (data.legacyHub) item { JNotice(stringResource(R.string.settings_diag_legacy_hub), status = Status.NeedsYou) }
        if (endpoints != null) editableCandidates(endpoints, scope)
        else {
            item { JSectionHeader(stringResource(R.string.settings_diag_candidates), count = data.candidates.size) }
            if (data.candidates.isEmpty()) item { JEmptyState(stringResource(R.string.settings_diag_waiting), stringResource(R.string.settings_diag_waiting_detail)) }
            data.candidates.forEach { candidate ->
                diagnosticGroup(routeKind(candidate.kind, resources), listOf(
                    resources.getString(R.string.settings_diag_address) to candidate.url,
                    resources.getString(R.string.settings_diag_last_probe) to candidate.result.resolve(resources).takeUnless { it.isBlank() },
                    resources.getString(R.string.settings_diag_probe_time) to candidate.probeMeasurement(resources),
                    resources.getString(R.string.settings_diag_probed_at) to localTime(candidate.checkedAt, resources.textLocale),
                ))
            }
        }
        diagnosticGroup(resources.getString(R.string.settings_diag_phone), listOf(resources.getString(R.string.settings_diag_network) to network,
            resources.getString(R.string.settings_diag_fingerprint) to data.fingerprint?.takeIf(String::isNotBlank)?.let { if (fingerprintExpanded) it else it.take(16) }))
        if (!data.fingerprint.isNullOrBlank()) item {
            Action(stringResource(if (fingerprintExpanded) R.string.settings_diag_fingerprint_less else R.string.settings_diag_fingerprint_more)) { fingerprintExpanded = !fingerprintExpanded }
        }
        diagnosticGroup(resources.getString(R.string.settings_background_title), background.rows(resources),
            note = resources.getString(R.string.settings_background_note))
        item {
            JButton(stringResource(if (backgroundCopied) R.string.settings_background_copied else R.string.settings_background_copy), {
                if (copy(background.copyText(resources), null)) backgroundCopied = true
            }, Modifier.fillMaxWidth(), kind = JButtonKind.Secondary, size = JButtonSize.Small)
        }
        fun seconds(value: Int?) = value?.let { resources.getQuantityString(R.plurals.settings_seconds, it, it) }
        diagnosticGroup(resources.getString(R.string.settings_diag_heartbeat), listOf(
            resources.getString(R.string.settings_diag_heartbeat_current) to seconds(data.heartbeatSeconds),
            resources.getString(R.string.settings_diag_heartbeat_foreground) to seconds(data.foregroundHeartbeatSeconds),
            resources.getString(R.string.settings_diag_heartbeat_background) to seconds(data.backgroundHeartbeatSeconds),
            resources.getString(R.string.settings_diag_pong_timeout) to seconds(data.pongTimeoutSeconds),
        ), note = resources.getString(R.string.settings_diag_heartbeat_note))
        item {
            JSectionHeader(stringResource(R.string.settings_diag_records))
            SettingsCaption(summaryText ?: stringResource(R.string.settings_diag_no_summary))
            if (summary != null) SettingsCaption(stringResource(R.string.settings_disconnects_note))
        }
        if (data.sessions.isEmpty()) item { JEmptyState(stringResource(R.string.settings_diag_no_records), stringResource(R.string.settings_diag_no_records_detail)) }
        data.sessions.forEach { session ->
            diagnosticGroup(resources.getString(R.string.settings_diag_session), listOf(
                resources.getString(R.string.settings_diag_connected_at) to localTime(session.connectedAt, resources.textLocale),
                resources.getString(R.string.settings_diag_disconnected_at) to (localTime(session.disconnectedAt, resources.textLocale) ?: resources.getString(R.string.settings_diag_still_connected)),
                resources.getString(R.string.settings_diag_disconnect_reason) to session.reason))
        }
        pairing?.takeUnless { it.failed }?.let { pairingAttempt(it, resources) }
        diagnosticGroup(resources.getString(R.string.settings_diag_cellular), listOf(resources.getString(R.string.settings_diag_cellular_result) to
            CellularTest.text(resources, context.getSharedPreferences("settings-readiness", 0).getString("cellular-test", null))))
        item {
            Column(Modifier.padding(vertical = Tokens.Space.m), verticalArrangement = Arrangement.spacedBy(Tokens.Space.m)) {
                SettingsCaption(stringResource(R.string.settings_diag_ddns))
                message?.let { JNotice(stringResource(it), status = Status.NeedsYou) }
                JButton(stringResource(R.string.settings_diag_reprobe), {
                    network = PhoneReadiness.network(context)
                    scope.launch { controller.refresh(reprobe = true) }
                    scope.launch { controller.refreshSummary() }
                }, Modifier.fillMaxWidth(), loading = busy, loadingText = stringResource(R.string.settings_reading_ellipsis))
                footer()
            }
        }
    }
    endpoints?.let { EndpointOverlays(it) }
}

/** The locale the text of these resources is in (the app's language). */
internal val Resources.textLocale: java.util.Locale get() = configuration.locales[0]

/**
 * Wire times are ISO-8601 UTC; show the phone's local time in the app's language (14:30:05 · 2:30:05 PM), with the
 * date only when it is not today. Text that is not a time is shown as is.
 */
internal fun localTime(value: String?, locale: java.util.Locale, zone: ZoneId = ZoneId.systemDefault(), today: LocalDate = LocalDate.now(zone)): String? {
    if (value.isNullOrBlank()) return value
    val instant = runCatching { Instant.parse(value) }.getOrNull()
        ?: runCatching { OffsetDateTime.parse(value).toInstant() }.getOrNull() ?: return value
    val time = instant.atZone(zone)
    val skeleton = if (time.toLocalDate() == today) LocalizedFormats.TIME_SECONDS else LocalizedFormats.MONTH_DAY_TIME_SECONDS
    return LocalizedFormats.format(instant, skeleton, locale, zone)
}

private fun LazyListScope.pairingAttempt(attempt: PairingAttempt, resources: Resources) {
    val time = resources.getString(R.string.settings_diag_time)
    diagnosticGroup(resources.getString(R.string.settings_diag_last_pairing),
        attempt.rows(resources).map { (label, value) -> label to if (label == time) localTime(value, resources.textLocale) else value })
    attempt.candidates.forEach { candidate ->
        diagnosticGroup(resources.getString(R.string.settings_diag_pairing_probe, routeKind(candidate.kind, resources)), listOf(
            resources.getString(R.string.settings_diag_address) to candidate.url,
            resources.getString(R.string.settings_diag_probe_result) to candidate.result.resolve(resources).takeUnless { it.isBlank() },
            resources.getString(R.string.settings_diag_probed_at) to localTime(candidate.checkedAt, resources.textLocale),
        ))
    }
}

private fun LazyListScope.diagnosticGroup(title: String, values: List<Pair<String, String?>>, note: String? = null) {
    item { JSectionHeader(title) }
    note?.let { item { SettingsCaption(it, Modifier.padding(bottom = Tokens.Space.s)) } }
    itemsIndexed(values) { index, (label, value) ->
        JGroupItem(GroupPosition.of(index, values.size)) { DiagnosticValue(label, value) }
    }
}

@Composable
internal fun DiagnosticValue(label: String, value: String?) {
    val shown = value?.takeIf(String::isNotBlank) ?: stringResource(R.string.settings_diag_no_data)
    if (shown.length > 36) {
        JRow(label)
        SelectionContainer {
            Text(shown, Modifier.fillMaxWidth().padding(horizontal = Tokens.Space.m).padding(bottom = Tokens.Space.m),
                style = PersonaTheme.type.micro, color = PersonaTheme.palette.ink2)
        }
    } else JRow(label, trailing = {
        SelectionContainer {
            Text(shown, style = PersonaTheme.type.micro, color = PersonaTheme.palette.ink2,
                modifier = Modifier.widthIn(max = 180.dp))
        }
    })
}

@Composable
internal fun ConnectionStatus(state: ConnectionState, paired: Boolean = true) {
    val status = when {
        !paired -> Status.Offline
        state == ConnectionState.Online -> Status.Unread
        state == ConnectionState.Connecting || state == ConnectionState.Updating -> Status.Working
        state == ConnectionState.WaitingForNetwork -> Status.Offline
        else -> Status.NeedsYou
    }
    Row(horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s), verticalAlignment = Alignment.CenterVertically) {
        JStatusDot(status)
        StatusLabel(status, text = if (paired) state.label() else stringResource(R.string.settings_connection_not_connected))
    }
}

@Composable
internal fun ConnectionState.label(): String = stringResource(when (this) {
    ConnectionState.Online -> R.string.settings_connection_online
    ConnectionState.Connecting -> R.string.settings_connection_connecting
    ConnectionState.Updating -> R.string.settings_connection_updating
    ConnectionState.WaitingForNetwork -> R.string.settings_connection_waiting_network
    ConnectionState.NeedsLocalNetworkPermission -> R.string.settings_connection_needs_local_network
    ConnectionState.CertificateMismatch -> R.string.settings_connection_certificate_changed
    ConnectionState.Revoked -> R.string.settings_connection_revoked
})

/** The result of "Checklist"'s cellular test, kept as a code (older builds kept the sentence itself). */
internal object CellularTest {
    const val Confirmed = "confirmed"

    fun text(resources: Resources, value: String?): String? = when (value) {
        Confirmed -> resources.getString(R.string.settings_ready_cellular_confirmed)
        else -> value
    }
}
