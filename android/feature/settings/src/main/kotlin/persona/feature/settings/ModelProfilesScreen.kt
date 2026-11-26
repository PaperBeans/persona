package persona.feature.settings

import android.content.res.Resources
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import persona.core.contracts.api.*
import persona.core.designsystem.*
import persona.core.designsystem.icons.PersonaIcons

/**
 * Model profiles section rendered inside Settings › Model configuration (aligned with Web's `ModelProfiles`).
 * Hidden cleanly on older hubs that do not expose `/api/v1/model-profiles`.
 */
internal fun LazyListScope.modelProfilesSection(
    res: Resources,
    state: ModelProfilesState,
    query: String,
    onSelect: (ModelProfile) -> Unit,
    onRollback: () -> Unit,
    onRetry: () -> Unit,
) {
    val view = state.view
    if (view == null) {
        val readError = state.error
        if (!state.unsupported && readError != null) {
            item("model-profiles-error") {
                JNotice(
                    readError.text(),
                    Modifier.padding(top = Tokens.Space.m),
                    status = Status.NeedsYou,
                    actionLabel = stringResource(R.string.settings_src_retry),
                    onAction = onRetry,
                )
            }
        }
        return
    }
    val q = query.trim()
    val profiles = if (q.isEmpty()) view.profiles else view.profiles.filter { profile ->
        listOfNotNull(profile.name, profile.id, profile.description).any { it.contains(q, ignoreCase = true) }
    }
    val showRollback = q.isEmpty() && view.rollback != null
    if (profiles.isEmpty() && !showRollback) return

    item("model-profiles-header") {
        JSectionHeader(stringResource(R.string.settings_model_profiles_title), count = profiles.size)
    }
    val totalRows = profiles.size + if (showRollback) 1 else 0
    itemsIndexed(profiles, key = { _, profile -> "model-profile-${profile.id}" }) { index, profile ->
        val subtitle = listOfNotNull(
            profile.description?.takeIf(String::isNotBlank),
            if (profile.changes.isEmpty()) res.getString(R.string.settings_model_profiles_no_changes)
            else res.getString(R.string.settings_model_profiles_changes, profile.changes.size),
        ).joinToString("\n")
        JGroupItem(GroupPosition.of(index, totalRows)) {
            JRow(
                title = profile.displayName,
                subtitle = subtitle,
                subtitleLines = 3,
                value = if (profile.active) stringResource(R.string.settings_model_profiles_active) else null,
                modifier = Modifier.testTag("model-profile-row-${profile.id}"),
                enabled = !state.busy,
                onClick = { onSelect(profile) },
            )
        }
    }
    if (showRollback) {
        val undo = requireNotNull(view.rollback)
        val targetName = undo.to?.takeIf(String::isNotBlank)?.let { targetId ->
            view.profiles.firstOrNull { it.id == targetId }?.displayName ?: targetId
        } ?: res.getString(R.string.settings_model_profiles_custom)
        item("model-profiles-rollback") {
            JGroupItem(GroupPosition.of(profiles.size, totalRows)) {
                JRow(
                    title = stringResource(R.string.settings_model_profiles_rollback),
                    subtitle = targetName,
                    leadingIcon = PersonaIcons.undo,
                    chevron = false,
                    modifier = Modifier.testTag("model-profile-rollback"),
                    trailing = {
                        JButton(
                            text = stringResource(R.string.settings_model_profiles_rollback),
                            onClick = onRollback,
                            kind = JButtonKind.Secondary,
                            size = JButtonSize.Small,
                            loading = state.busyAction == ModelProfileBusyAction.Rollback,
                            enabled = !state.busy,
                        )
                    },
                )
            }
        }
    }
    if (q.isEmpty()) {
        item("model-profiles-footer") {
            val footer = if (view.active.isNullOrBlank()) {
                "${stringResource(R.string.settings_model_profiles_footer)} ${stringResource(R.string.settings_model_profiles_none_active)}"
            } else {
                stringResource(R.string.settings_model_profiles_footer)
            }
            SettingsCaption(footer, Modifier.padding(horizontal = rowPadding, vertical = Tokens.Space.s))
        }
    }
    state.actionError?.let { error ->
        item("model-profiles-action-error") {
            JInlineStatus(error.text(), Modifier.padding(top = Tokens.Space.s), tone = InlineTone.Failed)
        }
    }
}

@Composable
private fun ProfileActionErrorBanner(
    error: ResText,
    conflict: Boolean,
    reloadLabel: String,
    busy: Boolean,
    onReload: () -> Unit,
    errorTag: String = "model-profile-action-error",
) {
    Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
        JInlineStatus(error.text(), Modifier.testTag(errorTag), tone = InlineTone.Failed)
        if (conflict) {
            JButton(
                text = reloadLabel,
                onClick = onReload,
                modifier = Modifier.fillMaxWidth().testTag("model-profile-reload-button"),
                kind = JButtonKind.Secondary,
                size = JButtonSize.Medium,
                enabled = !busy,
            )
        }
    }
}

/** Detail page for one model profile: inspect drift, Agent presets, purpose bindings, launcher overrides, capture, edit or apply. */
@Composable
internal fun ModelProfileDetailPage(
    profile: ModelProfile,
    catalog: ModelCatalog?,
    busyAction: ModelProfileBusyAction?,
    error: ResText?,
    conflict: Boolean,
    onBack: () -> Unit,
    onEdit: () -> Unit,
    onApply: () -> Unit,
    onCapture: () -> Unit,
    onReload: () -> Unit,
) {
    val res = LocalResources.current
    val busy = busyAction != null
    val providers = catalog?.providers.orEmpty()
    val catalogPurposes = catalog?.purposes.orEmpty()
    val tiers = remember(profile.tiers) { profile.tiers.entries.sortedBy { it.key } }
    val purposeEntries = remember(profile.purposes) {
        profile.purposes.entries.sortedWith(
            compareBy<Map.Entry<String, ModelBinding>> { entry ->
                val idx = ModelProfileRules.CATEGORY_IDS.indexOf(entry.key)
                if (idx >= 0) idx else Int.MAX_VALUE
            }.thenBy { it.key }
        )
    }
    val launcherEntries = remember(profile.launchers) { profile.launchers.entries.sortedBy { it.key } }
    val describedChanges = remember(profile, catalogPurposes, providers, res) {
        profile.changes.map { describeProfileChange(res, it, profile, catalogPurposes, providers) }
    }

    JListPage(
        title = profile.displayName,
        onBack = onBack,
        largeTitle = false,
        bottomBar = {
            Column(
                Modifier.fillMaxWidth().padding(horizontal = pagePadding, vertical = Tokens.Space.s)
                    .testTag("model-profile-detail-footer"),
                verticalArrangement = Arrangement.spacedBy(Tokens.Space.s),
            ) {
                error?.let { err ->
                    ProfileActionErrorBanner(
                        error = err,
                        conflict = conflict,
                        reloadLabel = stringResource(R.string.settings_model_profiles_reload),
                        busy = busy,
                        onReload = onReload,
                    )
                }
                Row(
                    Modifier.fillMaxWidth(),
                    horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s),
                ) {
                    JButton(
                        text = stringResource(R.string.settings_model_profiles_edit),
                        onClick = onEdit,
                        modifier = Modifier.weight(1f).testTag("model-profile-edit-button"),
                        kind = JButtonKind.Secondary,
                        enabled = !busy,
                    )
                    JButton(
                        text = stringResource(R.string.settings_model_profiles_apply),
                        onClick = onApply,
                        modifier = Modifier.weight(1f).testTag("model-profile-apply-button"),
                        loading = busyAction == ModelProfileBusyAction.Apply,
                        enabled = !busy,
                    )
                }
            }
        },
    ) {
        profile.description?.takeIf(String::isNotBlank)?.let { desc ->
            item("profile-description") {
                SettingsCaption(desc, Modifier.padding(horizontal = rowPadding, vertical = Tokens.Space.s))
            }
        }
        item("profile-summary") {
            val summaryText = if (profile.changes.isEmpty()) {
                stringResource(R.string.settings_model_profiles_no_changes)
            } else {
                stringResource(R.string.settings_model_profiles_changes, profile.changes.size)
            }
            JGroupItem(GroupPosition.Single) {
                JRow(
                    title = summaryText,
                    value = if (profile.active) stringResource(R.string.settings_model_profiles_active) else null,
                    chevron = false,
                )
            }
        }
        if (describedChanges.isNotEmpty()) {
            item("profile-changes") {
                Column(Modifier.padding(top = Tokens.Space.s).testTag("model-profile-changes")) {
                    describedChanges.forEachIndexed { index, change ->
                        JGroupItem(GroupPosition.of(index, describedChanges.size)) {
                            JRow(
                                title = change.label,
                                subtitle = change.target,
                                subtitleLines = 2,
                                chevron = false,
                            )
                        }
                    }
                }
            }
        }
        item("profile-capture") {
            JCard(
                modifier = Modifier.padding(top = Tokens.Space.s),
                status = if (profile.active && profile.changes.isNotEmpty()) Status.NeedsYou else null,
            ) {
                Text(
                    text = stringResource(
                        if (profile.active && profile.changes.isNotEmpty()) R.string.settings_model_profiles_active_drift
                        else R.string.settings_model_profiles_capture_hint,
                    ),
                    style = PersonaTheme.type.callout,
                    color = PersonaTheme.palette.ink2,
                )
                Spacer(Modifier.height(Tokens.Space.s))
                JButton(
                    text = stringResource(R.string.settings_model_profiles_capture_from_current),
                    onClick = onCapture,
                    modifier = Modifier.fillMaxWidth().testTag("model-profile-capture-button"),
                    kind = JButtonKind.Secondary,
                    size = JButtonSize.Medium,
                    loading = busyAction == ModelProfileBusyAction.Capture,
                    enabled = !busy,
                )
            }
        }

        // Agent presets (tiers)
        item("profile-tiers-header") {
            JSectionHeader(stringResource(R.string.settings_model_profiles_tiers), count = tiers.size)
        }
        if (tiers.isEmpty()) {
            item("profile-tiers-empty") {
                SettingsCaption(
                    stringResource(R.string.settings_model_profiles_no_tiers),
                    Modifier.padding(horizontal = rowPadding, vertical = Tokens.Space.xs),
                )
            }
        } else {
            itemsIndexed(tiers, key = { _, (name, _) -> "detail-tier-$name" }) { index, (name, tier) ->
                val launcherLabel = providers.firstOrNull { it.id == "launcher.${tier.launcher}" }?.displayName ?: tier.launcher
                val runs = listOfNotNull(
                    launcherLabel.takeIf(String::isNotBlank),
                    tier.model?.takeIf(String::isNotBlank),
                    tier.effort?.takeIf(String::isNotBlank)?.let { effortDisplayLabel(res, it) },
                    tier.permissionMode?.takeIf(String::isNotBlank)?.let { permissionModeDisplayLabel(res, it) },
                    tier.speed?.takeIf { it == "fast" || it == "ultrafast" }?.let { tierSpeedDisplayLabel(res, it) },
                ).joinToString(" · ")
                val subtitle = listOfNotNull(runs.takeIf(String::isNotBlank), tier.description?.takeIf(String::isNotBlank)).joinToString("\n")
                JGroupItem(GroupPosition.of(index, tiers.size)) {
                    JRow(title = name, subtitle = subtitle, subtitleLines = 3, chevron = false)
                }
            }
        }

        // Purpose bindings
        item("profile-purposes-header") {
            JSectionHeader(stringResource(R.string.settings_model_profiles_purposes_section), count = purposeEntries.size)
        }
        if (purposeEntries.isEmpty()) {
            item("profile-purposes-empty") {
                SettingsCaption(
                    stringResource(R.string.settings_model_profiles_no_purposes),
                    Modifier.padding(horizontal = rowPadding, vertical = Tokens.Space.xs),
                )
            }
        } else {
            itemsIndexed(purposeEntries, key = { _, (id, _) -> "detail-purpose-$id" }) { index, (id, binding) ->
                val providerLabel = binding.provider?.takeIf(String::isNotBlank)?.let { pid ->
                    providers.firstOrNull { it.id == pid }?.displayName ?: pid
                } ?: res.getString(R.string.settings_model_profiles_purpose_follow_default)
                val summary = listOfNotNull(
                    providerLabel,
                    binding.model?.takeIf(String::isNotBlank),
                    binding.effort?.takeIf(String::isNotBlank)?.let { effortDisplayLabel(res, it) },
                    binding.permissionMode?.takeIf(String::isNotBlank)?.let { permissionModeDisplayLabel(res, it) },
                ).joinToString(" · ")
                JGroupItem(GroupPosition.of(index, purposeEntries.size)) {
                    JRow(
                        title = profilePurposeLabel(res, id, catalogPurposes),
                        subtitle = summary,
                        subtitleLines = 2,
                        chevron = false,
                    )
                }
            }
        }

        // Managed provider overrides
        item("profile-launchers-header") {
            JSectionHeader(stringResource(R.string.settings_model_profiles_launchers_section), count = launcherEntries.size)
        }
        if (launcherEntries.isEmpty()) {
            item("profile-launchers-empty") {
                SettingsCaption(
                    stringResource(R.string.settings_model_profiles_no_launchers),
                    Modifier.padding(horizontal = rowPadding, vertical = Tokens.Space.xs),
                )
            }
        } else {
            itemsIndexed(launcherEntries, key = { _, (name, _) -> "detail-launcher-$name" }) { index, (name, models) ->
                val title = providers.firstOrNull { it.id == "launcher.$name" }?.displayName ?: name
                val lines = launcherModelEntries(models).map { (field, value) ->
                    val shown = when {
                        value.isEmpty() -> res.getString(R.string.settings_model_profiles_clear_on_switch)
                        field == "effort" -> effortDisplayLabel(res, value)
                        else -> value
                    }
                    "${launcherFieldDisplayLabel(res, field)}: $shown"
                }.joinToString("\n")
                JGroupItem(GroupPosition.of(index, launcherEntries.size)) {
                    JRow(title = title, subtitle = lines.ifEmpty { null }, subtitleLines = 8, chevron = false)
                }
            }
        }
    }
}

/** Pre-apply confirmation page listing drifted fields and offering capture-active-first before switching. */
@Composable
internal fun ModelProfileApplyConfirmPage(
    profile: ModelProfile,
    activeProfile: ModelProfile?,
    catalog: ModelCatalog?,
    busyAction: ModelProfileBusyAction?,
    error: ResText?,
    conflict: Boolean,
    onCaptureActiveAndApply: () -> Unit,
    onApplyDirect: () -> Unit,
    onCaptureCurrent: () -> Unit,
    onReload: () -> Unit,
    onCancel: () -> Unit,
) {
    val res = LocalResources.current
    val busy = busyAction != null
    val providers = catalog?.providers.orEmpty()
    val catalogPurposes = catalog?.purposes.orEmpty()
    val drift = remember(profile, activeProfile) { profileApplyDrift(profile, activeProfile) }
    val describedTargetChanges = remember(drift.targetChanges, profile, catalogPurposes, providers, res) {
        drift.targetChanges.map { describeProfileChange(res, it, profile, catalogPurposes, providers) }
    }
    val describedActiveDrift = remember(drift.activeDrift, activeProfile, catalogPurposes, providers, res) {
        drift.activeDrift.map { describeProfileChange(res, it, activeProfile, catalogPurposes, providers) }
    }

    JListPage(
        title = stringResource(R.string.settings_model_profiles_confirm_apply_title, profile.displayName),
        modifier = Modifier.testTag("model-profile-apply-confirm"),
        onBack = onCancel,
        largeTitle = false,
        bottomBar = {
            Column(
                Modifier.fillMaxWidth().padding(horizontal = pagePadding, vertical = Tokens.Space.s)
                    .testTag("model-profile-confirm-footer"),
                verticalArrangement = Arrangement.spacedBy(Tokens.Space.s),
            ) {
                error?.let { err ->
                    ProfileActionErrorBanner(
                        error = err,
                        conflict = conflict,
                        reloadLabel = stringResource(R.string.settings_model_profiles_reload),
                        busy = busy,
                        onReload = onReload,
                    )
                }
                when {
                    drift.canCaptureActiveFirst && activeProfile != null -> {
                        if (drift.hasActiveDrift) {
                            JButton(
                                text = stringResource(R.string.settings_model_profiles_capture_active_first, activeProfile.displayName),
                                onClick = onCaptureActiveAndApply,
                                modifier = Modifier.fillMaxWidth().testTag("model-profile-capture-and-apply"),
                                kind = JButtonKind.Primary,
                                loading = busyAction == ModelProfileBusyAction.CaptureAndApply,
                                enabled = !busy,
                            )
                            JButton(
                                text = stringResource(R.string.settings_model_profiles_apply_directly),
                                onClick = onApplyDirect,
                                modifier = Modifier.fillMaxWidth().testTag("model-profile-apply-direct"),
                                kind = JButtonKind.Secondary,
                                loading = busyAction == ModelProfileBusyAction.Apply,
                                enabled = !busy,
                            )
                        } else {
                            JButton(
                                text = stringResource(R.string.settings_model_profiles_apply_directly),
                                onClick = onApplyDirect,
                                modifier = Modifier.fillMaxWidth().testTag("model-profile-apply-direct"),
                                kind = JButtonKind.Primary,
                                loading = busyAction == ModelProfileBusyAction.Apply,
                                enabled = !busy,
                            )
                            JButton(
                                text = stringResource(R.string.settings_model_profiles_capture_active_first, activeProfile.displayName),
                                onClick = onCaptureActiveAndApply,
                                modifier = Modifier.fillMaxWidth().testTag("model-profile-capture-and-apply"),
                                kind = JButtonKind.Secondary,
                                loading = busyAction == ModelProfileBusyAction.CaptureAndApply,
                                enabled = !busy,
                            )
                        }
                    }
                    profile.active -> {
                        JButton(
                            text = stringResource(R.string.settings_model_profiles_capture_from_current),
                            onClick = onCaptureCurrent,
                            modifier = Modifier.fillMaxWidth().testTag("model-profile-confirm-capture"),
                            kind = JButtonKind.Primary,
                            loading = busyAction == ModelProfileBusyAction.Capture,
                            enabled = !busy,
                        )
                        JButton(
                            text = stringResource(R.string.settings_model_profiles_apply_directly),
                            onClick = onApplyDirect,
                            modifier = Modifier.fillMaxWidth().testTag("model-profile-apply-direct"),
                            kind = JButtonKind.Secondary,
                            loading = busyAction == ModelProfileBusyAction.Apply,
                            enabled = !busy,
                        )
                    }
                    else -> {
                        JButton(
                            text = stringResource(R.string.settings_model_profiles_apply_directly),
                            onClick = onApplyDirect,
                            modifier = Modifier.fillMaxWidth().testTag("model-profile-apply-direct"),
                            kind = JButtonKind.Primary,
                            loading = busyAction == ModelProfileBusyAction.Apply,
                            enabled = !busy,
                        )
                    }
                }
                JButton(
                    text = stringResource(R.string.settings_src_cancel),
                    onClick = onCancel,
                    modifier = Modifier.fillMaxWidth().testTag("model-profile-confirm-cancel"),
                    kind = JButtonKind.Quiet,
                    enabled = !busy,
                )
            }
        },
    ) {
        if (describedTargetChanges.isNotEmpty()) {
            item("confirm-intro") {
                SettingsCaption(
                    text = stringResource(
                        if (profile.active) R.string.settings_model_profiles_confirm_reapply_intro
                        else R.string.settings_model_profiles_confirm_apply_intro,
                        profile.displayName,
                    ),
                    modifier = Modifier.padding(horizontal = rowPadding, vertical = Tokens.Space.s),
                )
            }
            item("confirm-drift-list") {
                Column(Modifier.testTag("model-profile-apply-drift-list")) {
                    describedTargetChanges.forEachIndexed { index, change ->
                        JGroupItem(GroupPosition.of(index, describedTargetChanges.size)) {
                            JRow(
                                title = change.label,
                                subtitle = change.target,
                                subtitleLines = 2,
                                chevron = false,
                            )
                        }
                    }
                }
            }
        } else if (activeProfile != null && describedActiveDrift.isNotEmpty()) {
            item("confirm-active-drift-only") {
                SettingsCaption(
                    text = stringResource(
                        R.string.settings_model_profiles_confirm_switch_active_drift_only,
                        activeProfile.displayName,
                        profile.displayName,
                    ),
                    modifier = Modifier.padding(horizontal = rowPadding, vertical = Tokens.Space.s),
                )
            }
        }

        if (drift.canCaptureActiveFirst && activeProfile != null) {
            if (drift.hasActiveDrift) {
                item("confirm-active-drift-notice") {
                    JCard(
                        modifier = Modifier.padding(top = Tokens.Space.m).testTag("model-profile-active-drift-notice"),
                        status = Status.NeedsYou,
                    ) {
                        Text(
                            text = stringResource(
                                R.string.settings_model_profiles_confirm_active_drift,
                                activeProfile.displayName,
                            ),
                            style = PersonaTheme.type.callout,
                            color = PersonaTheme.palette.ink2,
                        )
                        Spacer(Modifier.height(Tokens.Space.s))
                        Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
                            describedActiveDrift.forEach { change ->
                                Text(
                                    text = change.target?.let { "${change.label}: $it" } ?: change.label,
                                    style = PersonaTheme.type.bodyStrong,
                                    color = PersonaTheme.palette.ink,
                                )
                            }
                        }
                    }
                }
            } else {
                item("confirm-capture-active-hint") {
                    SettingsCaption(
                        text = stringResource(
                            R.string.settings_model_profiles_confirm_capture_active_hint,
                            activeProfile.displayName,
                        ),
                        modifier = Modifier.padding(horizontal = rowPadding, vertical = Tokens.Space.m),
                    )
                }
            }
        }
    }
}

private sealed interface ProfilePicker {
    data class PurposeProvider(val purposeId: String) : ProfilePicker
    data class PurposeEffort(val purposeId: String) : ProfilePicker
    data class PurposePermissionMode(val purposeId: String) : ProfilePicker
    data class TierLauncher(val tierKey: String) : ProfilePicker
    data class TierEffort(val tierKey: String) : ProfilePicker
    data class TierSpeed(val tierKey: String) : ProfilePicker
    data class TierPermissionMode(val tierKey: String) : ProfilePicker
}

/** Interactive editor page for a ModelProfile (name, description, purpose bindings, and Agent presets/tiers). */
@Composable
internal fun ModelProfileEditorPage(
    profile: ModelProfile,
    revision: Long,
    syncKey: Int = 0,
    catalog: ModelCatalog?,
    busyAction: ModelProfileBusyAction?,
    error: ResText?,
    conflict: Boolean,
    onSave: (ModelProfileSaveInput) -> Unit,
    onCapture: () -> Unit,
    onReload: () -> Unit,
    onBack: () -> Unit,
) {
    val res = LocalResources.current
    val busy = busyAction != null
    val providers = catalog?.providers.orEmpty()
    val catalogPurposes = catalog?.purposes.orEmpty()
    val catalogIds = remember(catalogPurposes) { catalogPurposes.map { it.id } }
    val initialDraft = remember(profile.id, revision, syncKey, catalogIds) { profileToDraft(profile, catalogIds) }
    var draft by remember(profile.id, revision, syncKey) { mutableStateOf(initialDraft) }
    var tierCounter by remember(profile.id, revision, syncKey) { mutableIntStateOf(0) }
    var picker by remember { mutableStateOf<ProfilePicker?>(null) }

    val problems = remember(draft) { profileDraftProblems(draft) }
    val dirty = remember(draft, initialDraft) { isProfileDraftDirty(draft, initialDraft) }
    val canSave = !busy && !conflict && problems.isEmpty() && dirty
    val launcherProviders = remember(providers) {
        providers.filter { it.kind == "launcher" && it.id.startsWith("launcher.") && !it.hidden }
    }
    // Hide empty permission_mode pickers when the hub's launcher purposes omit permission_mode (cross-version hubs).
    val hubSupportsPermissionMode = remember(catalogPurposes) {
        val launcherPurposesWithFields = catalogPurposes.filter { it.kind == "launcher" && it.fields != null }
        launcherPurposesWithFields.isEmpty() || launcherPurposesWithFields.any { it.supports("permission_mode") }
    }

    fun updatePurpose(id: String, transform: (ModelProfilePurposeDraft) -> ModelProfilePurposeDraft) {
        val current = draft.purposes[id] ?: ModelProfilePurposeDraft()
        draft = draft.copy(purposes = draft.purposes + (id to transform(current)))
    }

    fun updateTier(key: String, transform: (ModelProfileTierDraft) -> ModelProfileTierDraft) {
        draft = draft.copy(tiers = draft.tiers.map { if (it.key == key) transform(it) else it })
    }

    JListPage(
        title = stringResource(R.string.settings_model_profiles_edit_title, profile.displayName),
        onBack = onBack,
        largeTitle = false,
        bottomBar = {
            Column(
                Modifier.fillMaxWidth().padding(horizontal = pagePadding, vertical = Tokens.Space.s)
                    .testTag("model-profile-editor-save-bar"),
                verticalArrangement = Arrangement.spacedBy(Tokens.Space.s),
            ) {
                if (problems.isNotEmpty()) {
                    Column(
                        Modifier.fillMaxWidth().heightIn(max = Tokens.Size.box120)
                            .verticalScroll(rememberScrollState())
                            .testTag("model-profile-editor-problems"),
                        verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs),
                    ) {
                        problems.forEach { problem ->
                            JInlineStatus(problem.text(), tone = InlineTone.Failed)
                        }
                    }
                }
                error?.let { err ->
                    ProfileActionErrorBanner(
                        error = err,
                        conflict = conflict,
                        reloadLabel = stringResource(R.string.settings_model_profiles_reload_discard),
                        busy = busy,
                        onReload = onReload,
                        errorTag = "model-profile-save-error",
                    )
                }
                JButton(
                    text = stringResource(R.string.settings_src_save),
                    onClick = { if (canSave) onSave(draftToProfileInput(draft, revision)) },
                    modifier = Modifier.fillMaxWidth().testTag("model-profile-save-button"),
                    loading = busyAction == ModelProfileBusyAction.Save,
                    enabled = canSave,
                )
            }
        },
    ) {
        item("editor-note") {
            JCard {
                Text(
                    text = stringResource(
                        if (profile.active) R.string.settings_model_profiles_save_note_active
                        else R.string.settings_model_profiles_save_note_inactive,
                    ),
                    style = PersonaTheme.type.callout,
                    color = PersonaTheme.palette.ink2,
                )
                Spacer(Modifier.height(Tokens.Space.s))
                JButton(
                    text = stringResource(R.string.settings_model_profiles_capture_from_current),
                    onClick = onCapture,
                    modifier = Modifier.fillMaxWidth().testTag("model-profile-editor-capture-button"),
                    kind = JButtonKind.Secondary,
                    size = JButtonSize.Medium,
                    loading = busyAction == ModelProfileBusyAction.Capture,
                    enabled = !busy,
                )
            }
        }
        item("editor-identity") {
            Column(Modifier.padding(top = Tokens.Space.m), verticalArrangement = Arrangement.spacedBy(Tokens.Space.m)) {
                JTextField(
                    value = draft.name,
                    onValueChange = { draft = draft.copy(name = it) },
                    modifier = Modifier.fillMaxWidth().testTag("model-profile-name-field"),
                    label = stringResource(R.string.settings_model_profiles_name),
                    placeholder = stringResource(R.string.settings_model_profiles_name_placeholder),
                    singleLine = true,
                    enabled = !busy,
                )
                JTextField(
                    value = draft.description,
                    onValueChange = { draft = draft.copy(description = it) },
                    modifier = Modifier.fillMaxWidth().testTag("model-profile-description-field"),
                    label = stringResource(R.string.settings_model_profiles_description),
                    placeholder = stringResource(R.string.settings_model_profiles_description_placeholder),
                    singleLine = true,
                    enabled = !busy,
                )
            }
        }

        // Purpose bindings
        item("editor-purposes-header") {
            JSectionHeader(stringResource(R.string.settings_model_profiles_purposes_section))
        }
        item("editor-purposes-hint") {
            SettingsCaption(
                stringResource(R.string.settings_model_profiles_purposes_hint),
                Modifier.padding(horizontal = rowPadding, vertical = Tokens.Space.xs),
            )
        }
        val purposeIds = draft.purposes.keys.toList()
        itemsIndexed(purposeIds, key = { _, id -> "editor-purpose-$id" }) { _, id ->
            val entry = draft.purposes.getValue(id)
            val title = profilePurposeLabel(res, id, catalogPurposes)
            val catalogPurpose = catalogPurposes.firstOrNull { it.id == id }
            val isLauncherPurpose = catalogPurpose?.kind == "launcher" ||
                id == "assistant" || id == "agent.new"
            val showPermissionMode = isLauncherPurpose && (
                entry.permissionMode.isNotBlank() ||
                    if (catalogPurpose?.fields != null) catalogPurpose.supports("permission_mode")
                    else hubSupportsPermissionMode
            )
            Column(Modifier.padding(top = Tokens.Space.s), verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
                JGroupItem(GroupPosition.Single) {
                    JSwitchRow(
                        title = title,
                        checked = entry.included,
                        onCheckedChange = { checked -> updatePurpose(id) { it.copy(included = checked) } },
                        subtitle = if (entry.included) stringResource(R.string.settings_model_profiles_purpose_included)
                        else stringResource(R.string.settings_model_profiles_purpose_unchanged),
                        enabled = !busy,
                    )
                }
                if (entry.included) {
                    val providerLabel = entry.provider.takeIf(String::isNotBlank)?.let { pid ->
                        providers.firstOrNull { it.id == pid }?.displayName ?: pid
                    } ?: stringResource(R.string.settings_model_profiles_purpose_follow_default)
                    val effortLabel = entry.effort.takeIf(String::isNotBlank)?.let { effortDisplayLabel(res, it) }
                        ?: stringResource(R.string.settings_model_profiles_purpose_follow_default)
                    val modeLabel = entry.permissionMode.takeIf(String::isNotBlank)?.let { permissionModeDisplayLabel(res, it) }
                        ?: stringResource(R.string.settings_model_profiles_purpose_follow_default)
                    val rowCount = if (showPermissionMode) 3 else 2
                    JGroupItem(GroupPosition.of(0, rowCount)) {
                        JRow(
                            title = stringResource(R.string.settings_src_model_provider),
                            value = providerLabel,
                            enabled = !busy,
                            onClick = { picker = ProfilePicker.PurposeProvider(id) },
                        )
                    }
                    JGroupItem(GroupPosition.of(1, rowCount)) {
                        JRow(
                            title = stringResource(R.string.settings_src_model_effort),
                            value = effortLabel,
                            enabled = !busy,
                            onClick = { picker = ProfilePicker.PurposeEffort(id) },
                        )
                    }
                    if (showPermissionMode) {
                        JGroupItem(GroupPosition.of(2, rowCount)) {
                            JRow(
                                title = stringResource(R.string.settings_model_profiles_permission_mode),
                                value = modeLabel,
                                modifier = Modifier.testTag("purpose-permission-mode-$id"),
                                enabled = !busy,
                                onClick = { picker = ProfilePicker.PurposePermissionMode(id) },
                            )
                        }
                    }
                    JTextField(
                        value = entry.model,
                        onValueChange = { value -> updatePurpose(id) { it.copy(model = value) } },
                        modifier = Modifier.fillMaxWidth().padding(top = Tokens.Space.xs),
                        label = stringResource(R.string.settings_src_model_model),
                        placeholder = stringResource(R.string.settings_model_profiles_purpose_follow_default),
                        singleLine = true,
                        enabled = !busy,
                    )
                }
            }
        }

        // Agent presets (tiers)
        item("editor-tiers-header") {
            JSectionHeader(
                stringResource(R.string.settings_model_profiles_tiers_count, draft.tiers.size, ModelProfileRules.MAX_MODEL_TIERS),
            )
        }
        item("editor-tiers-hint") {
            SettingsCaption(
                stringResource(R.string.settings_model_profiles_tiers_hint),
                Modifier.padding(horizontal = rowPadding, vertical = Tokens.Space.xs),
            )
        }
        if (draft.tiers.isEmpty()) {
            item("editor-tiers-empty") {
                SettingsCaption(
                    stringResource(R.string.settings_model_profiles_no_tiers),
                    Modifier.padding(horizontal = rowPadding, vertical = Tokens.Space.xs),
                )
            }
        } else {
            itemsIndexed(draft.tiers, key = { _, tier -> "editor-tier-${tier.key}" }) { _, tier ->
                val launcherLabel = tier.launcher.takeIf(String::isNotBlank)?.let { l ->
                    providers.firstOrNull { it.id == "launcher.$l" }?.displayName ?: l
                } ?: stringResource(R.string.settings_model_profiles_tier_launcher_placeholder)
                val effortLabel = tier.effort.takeIf(String::isNotBlank)?.let { effortDisplayLabel(res, it) }
                    ?: stringResource(R.string.settings_model_profiles_purpose_follow_default)
                val speedLabel = tierSpeedDisplayLabel(res, tier.speed)
                val modeLabel = tier.permissionMode.takeIf(String::isNotBlank)?.let { permissionModeDisplayLabel(res, it) }
                    ?: stringResource(R.string.settings_model_profiles_purpose_follow_default)
                val showTierPermissionMode = hubSupportsPermissionMode || tier.permissionMode.isNotBlank()
                val tierRowCount = if (showTierPermissionMode) 4 else 3
                Column(Modifier.padding(top = Tokens.Space.m), verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
                    JTextField(
                        value = tier.name,
                        onValueChange = { value -> updateTier(tier.key) { it.copy(name = value) } },
                        modifier = Modifier.fillMaxWidth().testTag("tier-name-${tier.key}"),
                        label = stringResource(R.string.settings_model_profiles_tier_name),
                        placeholder = stringResource(R.string.settings_model_profiles_tier_name_placeholder),
                        singleLine = true,
                        enabled = !busy,
                    )
                    JGroupItem(GroupPosition.of(0, tierRowCount)) {
                        JRow(
                            title = stringResource(R.string.settings_model_profiles_tier_launcher),
                            value = launcherLabel,
                            enabled = !busy,
                            onClick = { picker = ProfilePicker.TierLauncher(tier.key) },
                        )
                    }
                    JGroupItem(GroupPosition.of(1, tierRowCount)) {
                        JRow(
                            title = stringResource(R.string.settings_src_model_effort),
                            value = effortLabel,
                            enabled = !busy,
                            onClick = { picker = ProfilePicker.TierEffort(tier.key) },
                        )
                    }
                    JGroupItem(GroupPosition.of(2, tierRowCount)) {
                        JRow(
                            title = stringResource(R.string.settings_model_profiles_tier_speed),
                            value = speedLabel,
                            enabled = !busy,
                            onClick = { picker = ProfilePicker.TierSpeed(tier.key) },
                        )
                    }
                    if (showTierPermissionMode) {
                        JGroupItem(GroupPosition.of(3, tierRowCount)) {
                            JRow(
                                title = stringResource(R.string.settings_model_profiles_permission_mode),
                                value = modeLabel,
                                modifier = Modifier.testTag("tier-permission-mode-${tier.key}"),
                                enabled = !busy,
                                onClick = { picker = ProfilePicker.TierPermissionMode(tier.key) },
                            )
                        }
                    }
                    JTextField(
                        value = tier.model,
                        onValueChange = { value -> updateTier(tier.key) { it.copy(model = value) } },
                        modifier = Modifier.fillMaxWidth().padding(top = Tokens.Space.xs),
                        label = stringResource(R.string.settings_src_model_model),
                        placeholder = stringResource(R.string.settings_src_model_provider_default),
                        singleLine = true,
                        enabled = !busy,
                    )
                    JTextField(
                        value = tier.description,
                        onValueChange = { value -> updateTier(tier.key) { it.copy(description = value) } },
                        modifier = Modifier.fillMaxWidth(),
                        label = stringResource(R.string.settings_model_profiles_tier_description),
                        placeholder = stringResource(R.string.settings_model_profiles_tier_description_placeholder),
                        singleLine = true,
                        enabled = !busy,
                    )
                    JButton(
                        text = stringResource(R.string.settings_model_profiles_remove),
                        onClick = { draft = draft.copy(tiers = draft.tiers.filterNot { it.key == tier.key }) },
                        kind = JButtonKind.Secondary,
                        size = JButtonSize.Small,
                        enabled = !busy,
                    )
                }
            }
        }
        item("editor-add-tier") {
            JButton(
                text = stringResource(R.string.settings_model_profiles_add_tier),
                onClick = {
                    if (draft.tiers.size < ModelProfileRules.MAX_MODEL_TIERS) {
                        val defaultLauncher = launcherProviders.firstOrNull()?.id?.removePrefix("launcher.") ?: "claude"
                        val key = "new-tier-${++tierCounter}"
                        draft = draft.copy(
                            tiers = draft.tiers + ModelProfileTierDraft(key = key, launcher = defaultLauncher),
                        )
                    }
                },
                modifier = Modifier.fillMaxWidth().padding(top = Tokens.Space.s).testTag("model-profile-add-tier"),
                kind = JButtonKind.Secondary,
                enabled = !busy && draft.tiers.size < ModelProfileRules.MAX_MODEL_TIERS,
            )
        }
    }

    // Picker action sheets
    when (val activePicker = picker) {
        is ProfilePicker.PurposeProvider -> {
            val purposeId = activePicker.purposeId
            val current = draft.purposes[purposeId] ?: ModelProfilePurposeDraft()
            val catalogPurpose = catalogPurposes.firstOrNull { it.id == purposeId }
            val candidates = catalogPurpose?.providersFrom(providers) ?: providers.filterNot { it.hidden }
            val actions = listOf(
                JAction(
                    stringResource(R.string.settings_model_profiles_purpose_follow_default),
                    checked = current.provider.isEmpty(),
                    onClick = { updatePurpose(purposeId) { it.copy(provider = "", model = "", effort = "", permissionMode = "") } },
                )
            ) + candidates.map { p ->
                JAction(
                    p.displayName,
                    checked = current.provider == p.id,
                    onClick = {
                        updatePurpose(purposeId) {
                            if (it.provider == p.id) it else it.copy(provider = p.id, model = "", effort = "", permissionMode = "")
                        }
                    },
                )
            }
            JActionSheet(actions, onDismiss = { picker = null }, title = stringResource(R.string.settings_src_model_provider))
        }
        is ProfilePicker.PurposeEffort -> {
            val purposeId = activePicker.purposeId
            val current = draft.purposes[purposeId] ?: ModelProfilePurposeDraft()
            val actions = listOf(
                JAction(
                    stringResource(R.string.settings_model_profiles_purpose_follow_default),
                    checked = current.effort.isEmpty(),
                    onClick = { updatePurpose(purposeId) { it.copy(effort = "") } },
                )
            ) + ModelProfileRules.EFFORT_LEVELS.map { level ->
                JAction(
                    effortDisplayLabel(res, level),
                    checked = current.effort == level,
                    onClick = { updatePurpose(purposeId) { it.copy(effort = level) } },
                )
            }
            JActionSheet(actions, onDismiss = { picker = null }, title = stringResource(R.string.settings_src_model_effort))
        }
        is ProfilePicker.PurposePermissionMode -> {
            val purposeId = activePicker.purposeId
            val current = draft.purposes[purposeId] ?: ModelProfilePurposeDraft()
            val actions = listOf(
                JAction(
                    stringResource(R.string.settings_model_profiles_purpose_follow_default),
                    checked = current.permissionMode.isEmpty(),
                    onClick = { updatePurpose(purposeId) { it.copy(permissionMode = "") } },
                )
            ) + ModelProfileRules.PERMISSION_MODES.map { mode ->
                JAction(
                    permissionModeDisplayLabel(res, mode),
                    checked = current.permissionMode == mode,
                    onClick = { updatePurpose(purposeId) { it.copy(permissionMode = mode) } },
                )
            }
            JActionSheet(actions, onDismiss = { picker = null }, title = stringResource(R.string.settings_model_profiles_permission_mode))
        }
        is ProfilePicker.TierLauncher -> {
            val tierKey = activePicker.tierKey
            val current = draft.tiers.firstOrNull { it.key == tierKey }
            val launcherNames = buildList {
                launcherProviders.forEach { add(it.id.removePrefix("launcher.") to it.displayName) }
                if (none { it.first == "claude" }) add("claude" to "Claude Code")
                if (current != null && current.launcher.isNotBlank() && none { it.first == current.launcher }) {
                    add(current.launcher to current.launcher)
                }
            }
            val actions = launcherNames.map { (id, label) ->
                JAction(
                    label,
                    checked = current?.launcher == id,
                    onClick = {
                        updateTier(tierKey) {
                            if (it.launcher == id) it else it.copy(launcher = id, model = "", effort = "", permissionMode = "")
                        }
                    },
                )
            }
            JActionSheet(actions, onDismiss = { picker = null }, title = stringResource(R.string.settings_model_profiles_tier_launcher))
        }
        is ProfilePicker.TierEffort -> {
            val tierKey = activePicker.tierKey
            val current = draft.tiers.firstOrNull { it.key == tierKey }
            val actions = listOf(
                JAction(
                    stringResource(R.string.settings_model_profiles_purpose_follow_default),
                    checked = current?.effort.isNullOrEmpty(),
                    onClick = { updateTier(tierKey) { it.copy(effort = "") } },
                )
            ) + ModelProfileRules.EFFORT_LEVELS.map { level ->
                JAction(
                    effortDisplayLabel(res, level),
                    checked = current?.effort == level,
                    onClick = { updateTier(tierKey) { it.copy(effort = level) } },
                )
            }
            JActionSheet(actions, onDismiss = { picker = null }, title = stringResource(R.string.settings_src_model_effort))
        }
        is ProfilePicker.TierSpeed -> {
            val tierKey = activePicker.tierKey
            val current = draft.tiers.firstOrNull { it.key == tierKey }
            val actions = ModelProfileRules.TIER_SPEEDS.map { speed ->
                JAction(
                    tierSpeedDisplayLabel(res, speed),
                    checked = (current?.speed.orEmpty().let { if (it == "standard") "" else it }) == speed,
                    onClick = { updateTier(tierKey) { it.copy(speed = speed) } },
                )
            }
            JActionSheet(actions, onDismiss = { picker = null }, title = stringResource(R.string.settings_model_profiles_tier_speed))
        }
        is ProfilePicker.TierPermissionMode -> {
            val tierKey = activePicker.tierKey
            val current = draft.tiers.firstOrNull { it.key == tierKey }
            val actions = listOf(
                JAction(
                    stringResource(R.string.settings_model_profiles_purpose_follow_default),
                    checked = current?.permissionMode.isNullOrEmpty(),
                    onClick = { updateTier(tierKey) { it.copy(permissionMode = "") } },
                )
            ) + ModelProfileRules.PERMISSION_MODES.map { mode ->
                JAction(
                    permissionModeDisplayLabel(res, mode),
                    checked = current?.permissionMode == mode,
                    onClick = { updateTier(tierKey) { it.copy(permissionMode = mode) } },
                )
            }
            JActionSheet(actions, onDismiss = { picker = null }, title = stringResource(R.string.settings_model_profiles_permission_mode))
        }
        null -> Unit
    }
}
