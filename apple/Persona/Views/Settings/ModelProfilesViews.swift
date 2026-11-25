import SwiftUI

struct ModelProfileSheetRoute: Hashable, Identifiable {
    var id: String
}

/// Model profiles section inside Settings › Models (`ModelConfigurationView`).
/// Hidden cleanly on older hubs that do not expose `/api/v1/model-profiles`.
struct ModelProfilesSection: View {
    var profiles: ModelProfilesModel
    var onSelect: (ModelProfile) -> Void
    var onRollback: () -> Void
    var onRetry: () -> Void

    var body: some View {
        if let catalog = profiles.catalog {
            if !catalog.profiles.isEmpty || catalog.rollback != nil {
                Section {
                    ForEach(catalog.profiles) { profile in
                        Button {
                            profiles.clearErrors()
                            onSelect(profile)
                        } label: {
                            profileRow(profile)
                        }
                        .buttonStyle(.plain)
                        .disabled(profiles.busy)
                        .accessibilityIdentifier("model-profile-row-\(profile.id)")
                    }
                    if let undo = catalog.rollback {
                        rollbackRow(undo, profiles: catalog.profiles)
                    }
                    if let error = profiles.actionError {
                        Label(error, systemImage: Theme.Icon.error)
                            .font(Theme.Font.label)
                            .foregroundStyle(Theme.danger)
                            .accessibilityIdentifier("model-profiles-action-error")
                    }
                } header: {
                    Text("Model Profiles")
                } footer: {
                    Text(sectionFooter(catalog))
                        .font(Theme.Font.caption)
                }
            }
        } else if !profiles.unsupported, let error = profiles.error {
            Section {
                Label(error, systemImage: Theme.Icon.error)
                    .font(Theme.Font.label)
                    .foregroundStyle(Theme.danger)
                Button("Retry [Components b8784c]") { onRetry() }
                    .lineLimit(1)
                    .accessibilityIdentifier("model-profiles-retry")
            } header: {
                Text("Model Profiles")
            }
        }
    }

    private func profileRow(_ profile: ModelProfile) -> some View {
        HStack(spacing: Theme.Space.m) {
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                HStack(spacing: Theme.Space.s) {
                    Text(profile.displayName)
                        .font(Theme.Font.body)
                        .foregroundStyle(.primary)
                        .lineLimit(1)
                    if profile.active {
                        Chip(text: String(localized: "Active [Model Profiles]"), look: .tint(Theme.positive), compact: true)
                    }
                }
                if let description = ModelProfilesLogic.trim(profile.description) {
                    Text(description)
                        .font(Theme.Font.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(2)
                }
                Text(changeSummary(profile))
                    .font(Theme.Font.label)
                    .foregroundStyle(profile.active && !profile.changes.isEmpty ? Theme.cautionText : .secondary)
                    .lineLimit(1)
            }
            Spacer(minLength: Theme.Space.s)
            ThemedIcon(Theme.Icon.disclosure)
                .font(Theme.Font.caption)
                .foregroundStyle(.tertiary)
        }
        .padding(.vertical, Theme.Space.xxs)
        .contentShape(.rect)
    }

    private func rollbackRow(_ undo: ModelProfileUndo, profiles list: [ModelProfile]) -> some View {
        let targetName = ModelProfilesLogic.trim(undo.to).map { targetID in
            list.first(where: { $0.id == targetID })?.displayName ?? targetID
        } ?? String(localized: "Custom")
        return HStack(spacing: Theme.Space.m) {
            ThemedIcon(Theme.Icon.undo)
                .foregroundStyle(.secondary)
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                Text("Roll Back [Model Profiles]")
                    .font(Theme.Font.body)
                    .lineLimit(1)
                Text(targetName)
                    .font(Theme.Font.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            Spacer(minLength: Theme.Space.s)
            Button {
                onRollback()
            } label: {
                if profiles.busyAction == .rollback {
                    ProgressView().controlSize(.small)
                } else {
                    Text("Roll Back [Model Profiles]").buttonLabelLine()
                }
            }
            .themedButton()
            .controlSize(.small)
            .disabled(profiles.busy)
            .accessibilityIdentifier("model-profile-rollback")
        }
        .padding(.vertical, Theme.Space.xxs)
    }

    private func changeSummary(_ profile: ModelProfile) -> String {
        profile.changes.isEmpty
            ? String(localized: "Matches current settings [Model Profiles]")
            : String(localized: "\(profile.changes.count) changes on switch [Model Profiles]")
    }

    private func sectionFooter(_ catalog: ModelProfilesCatalog) -> String {
        let base = String(localized: "Profiles group purpose bindings, launcher overrides, and Agent presets so you can switch together. Credentials are never stored in profiles.")
        if ModelProfilesLogic.trim(catalog.active) == nil {
            return base + " " + String(localized: "No profile is active yet; Agent presets are off until you apply one.")
        }
        return base
    }
}

/// Sheet hosting profile inspection, pre-apply drift preview, and interactive editing.
struct ModelProfileSheet: View {
    var profileID: String
    var profiles: ModelProfilesModel
    var purposes: [ModelPurpose]
    var providers: [ModelProvider]
    var onChanged: () async -> Void
    var onClose: () -> Void

    @Environment(AppModel.self) private var app
    @State private var editing = false
    @State private var confirmingApply = false
    @State private var editRevision: Int64 = 0

    private var profile: ModelProfile? { profiles.profile(id: profileID) }

    var body: some View {
        NavigationStack {
            Group {
                if let profile {
                    ModelProfileDetailView(
                        profile: profile,
                        activeProfile: profiles.activeProfile,
                        profiles: profiles,
                        purposes: purposes,
                        providers: providers,
                        onEdit: {
                            profiles.clearErrors()
                            confirmingApply = false
                            editRevision = profiles.catalog?.revision ?? 0
                            editing = true
                        },
                        onRequestApply: { requestApply(profile) },
                        onCapture: { Task { await captureFromDetail(profile) } },
                        onReload: { Task { await reloadCatalog() } }
                    )
                    .pageToolbar {
                        SheetClose(disabled: profiles.busy) {
                            profiles.clearErrors()
                            onClose()
                        }
                    }
                    .navigationDestination(isPresented: $confirmingApply) {
                        if let current = profiles.profile(id: profileID) {
                            ModelProfileApplyConfirmView(
                                profile: current,
                                activeProfile: profiles.activeProfile,
                                profiles: profiles,
                                purposes: purposes,
                                providers: providers,
                                onCaptureActiveAndApply: { active in
                                    Task { await captureActiveAndApply(active: active, target: current) }
                                },
                                onApplyDirect: { Task { await applyDirect(current) } },
                                onCaptureCurrent: { Task { await captureFromConfirm(current) } },
                                onReload: { Task { await reloadCatalog() } },
                                onCancel: {
                                    guard !profiles.busy else { return }
                                    confirmingApply = false
                                    profiles.clearErrors()
                                }
                            )
                        }
                    }
                    .navigationDestination(isPresented: $editing) {
                        if let current = profiles.profile(id: profileID) {
                            ModelProfileEditorView(
                                profile: current,
                                revision: editRevision,
                                syncKey: profiles.syncKey,
                                profiles: profiles,
                                purposes: purposes,
                                providers: providers,
                                onSave: { input in Task { await saveEdit(current, input: input) } },
                                onCapture: { Task { await captureFromEditor(current) } },
                                onReload: { Task { await reloadForEdit() } }
                            )
                        }
                    }
                } else {
                    EmptyStateView(
                        symbol: Theme.Icon.model,
                        title: String(localized: "Profile Not Found [Model Profiles]"),
                        message: String(localized: "Reload Model Profiles to see the latest list.")
                    )
                    .pageToolbar {
                        SheetClose { onClose() }
                    }
                }
            }
        }
        .interactiveDismissDisabled(profiles.busy)
    }

    private func requestApply(_ profile: ModelProfile) {
        guard !profiles.busy else { return }
        if ModelProfilesLogic.shouldConfirmApply(target: profile, active: profiles.activeProfile) {
            profiles.clearErrors()
            confirmingApply = true
        } else {
            Task { await applyDirect(profile) }
        }
    }

    private func applyDirect(_ profile: ModelProfile) async {
        if await profiles.apply(id: profile.id) {
            Haptics.success()
            app.toast = Toast(String(localized: "Applied \(profile.displayName) [Model Profiles]"), style: .success)
            confirmingApply = false
            editing = false
            await onChanged()
            onClose()
        } else {
            Haptics.error()
        }
    }

    private func captureFromDetail(_ profile: ModelProfile) async {
        if await profiles.capture(profile) {
            Haptics.success()
            app.toast = Toast(String(localized: "Profile updated from current configuration"), style: .success)
            await onChanged()
        } else {
            Haptics.error()
        }
    }

    private func captureFromConfirm(_ profile: ModelProfile) async {
        if await profiles.capture(profile) {
            Haptics.success()
            app.toast = Toast(String(localized: "Profile updated from current configuration"), style: .success)
            confirmingApply = false
            await onChanged()
        } else {
            Haptics.error()
        }
    }

    private func captureFromEditor(_ profile: ModelProfile) async {
        if await profiles.capture(profile, revision: editRevision) {
            Haptics.success()
            editRevision = profiles.catalog?.revision ?? editRevision
            app.toast = Toast(String(localized: "Profile updated from current configuration"), style: .success)
            await onChanged()
        } else {
            Haptics.error()
        }
    }

    private func captureActiveAndApply(active: ModelProfile, target: ModelProfile) async {
        switch await profiles.captureActiveAndApply(active: active, target: target) {
        case .applied:
            Haptics.success()
            app.toast = Toast(String(localized: "Captured current into \(active.displayName) and applied \(target.displayName)"), style: .success)
            confirmingApply = false
            editing = false
            await onChanged()
            onClose()
        case .applyFailedAfterCapture:
            Haptics.error()
            await onChanged()
        case .captureFailed:
            Haptics.error()
        }
    }

    private func saveEdit(_ profile: ModelProfile, input: ModelProfileSaveInput) async {
        if await profiles.save(id: profile.id, input: input) {
            Haptics.success()
            editRevision = profiles.catalog?.revision ?? editRevision
            editing = false
            app.toast = Toast(String(localized: "Profile saved [Model Profiles]"), style: .success)
            await onChanged()
        } else {
            Haptics.error()
        }
    }

    private func reloadCatalog() async {
        profiles.clearErrors()
        _ = await profiles.load()
    }

    private func reloadForEdit() async {
        profiles.clearErrors()
        if let fresh = await profiles.load() {
            editRevision = fresh.revision
        }
    }
}

/// Adjacent error banner placed inside pinned bottom bars directly above Save / Apply actions.
private struct ProfileFooterErrorBanner: View {
    var error: String
    var conflict: Bool
    var reloadTitle: String
    var busy: Bool
    var errorID: String
    var onReload: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            HStack(alignment: .firstTextBaseline, spacing: Theme.Space.s) {
                ThemedIcon(Theme.Icon.error)
                    .font(Theme.Font.labelStrong)
                    .foregroundStyle(Theme.danger)
                Text(error)
                    .font(Theme.Font.label)
                    .foregroundStyle(Theme.danger)
                    .fixedSize(horizontal: false, vertical: true)
                    .accessibilityIdentifier(errorID)
            }
            if conflict {
                Button(action: onReload) {
                    Text(reloadTitle).buttonLabelLine().frame(maxWidth: .infinity)
                }
                .themedButton()
                .controlSize(.small)
                .disabled(busy)
                .accessibilityIdentifier("model-profile-reload-button")
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

/// Detail view for one model profile: drift summary, Update from current configuration, Agent presets,
/// purpose bindings, launcher overrides, and pinned Edit / Apply controls.
struct ModelProfileDetailView: View {
    var profile: ModelProfile
    var activeProfile: ModelProfile?
    var profiles: ModelProfilesModel
    var purposes: [ModelPurpose]
    var providers: [ModelProvider]
    var onEdit: () -> Void
    var onRequestApply: () -> Void
    var onCapture: () -> Void
    var onReload: () -> Void

    var body: some View {
        let describedChanges = profile.changes.map {
            ModelProfilesLogic.describeChange($0, profile: profile, purposes: purposes, providers: providers)
        }
        let sortedTiers = profile.tiers.sorted(by: { $0.key < $1.key })
        let sortedPurposes = ModelProfilesLogic.sortedPurposes(of: profile)
        let sortedLaunchers = profile.launchers.sorted(by: { $0.key < $1.key })

        SettingsForm {
            Section {
                if let description = ModelProfilesLogic.trim(profile.description) {
                    Text(description)
                        .font(Theme.Font.secondary)
                        .foregroundStyle(.secondary)
                }
                HStack(spacing: Theme.Space.s) {
                    Text(profile.changes.isEmpty
                         ? String(localized: "Matches current settings [Model Profiles]")
                         : String(localized: "\(profile.changes.count) changes on switch [Model Profiles]"))
                        .font(Theme.Font.body)
                    Spacer(minLength: Theme.Space.s)
                    if profile.active {
                        Chip(text: String(localized: "Active [Model Profiles]"), look: .tint(Theme.positive), compact: true)
                    }
                }
                if !describedChanges.isEmpty {
                    ForEach(describedChanges) { change in
                        VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                            Text(change.label)
                                .font(Theme.Font.secondaryStrong)
                            if let target = change.target {
                                Text(target)
                                    .font(Theme.Font.caption)
                                    .foregroundStyle(.secondary)
                            }
                        }
                        .accessibilityIdentifier("model-profile-change-\(change.id)")
                    }
                    .accessibilityIdentifier("model-profile-changes")
                }
            }

            Section {
                VStack(alignment: .leading, spacing: Theme.Space.s) {
                    Text(profile.active && !profile.changes.isEmpty
                         ? String(localized: "Current settings differ from this active profile. Update this profile from current configuration to keep them, or apply it again to restore it.")
                         : String(localized: "Replace this profile's purpose bindings and launcher overrides with your current configuration. Agent presets stay unchanged."))
                        .font(Theme.Font.secondary)
                        .foregroundStyle(profile.active && !profile.changes.isEmpty ? Theme.cautionText : .secondary)
                        .fixedSize(horizontal: false, vertical: true)
                    Button(action: onCapture) {
                        Group {
                            if profiles.busyAction == .capture {
                                ProgressView()
                            } else {
                                Text("Update from current configuration").buttonLabelLine()
                            }
                        }
                        .frame(maxWidth: .infinity)
                    }
                    .themedButton()
                    .disabled(profiles.busy)
                    .accessibilityIdentifier("model-profile-capture-button")
                }
                .padding(.vertical, Theme.Space.xxs)
            }

            Section("Agent Presets [Model Profiles]") {
                if sortedTiers.isEmpty {
                    Text("No Agent presets defined in this profile.")
                        .font(Theme.Font.secondary)
                        .foregroundStyle(.secondary)
                } else {
                    ForEach(sortedTiers, id: \.key) { name, tier in
                        VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                            Text(name).font(Theme.Font.bodyStrong)
                            let runs = ModelProfilesLogic.tierRunsSummary(tier, providers: providers)
                            if !runs.isEmpty {
                                Text(runs).font(Theme.Font.caption).foregroundStyle(.secondary)
                            }
                            if let desc = ModelProfilesLogic.trim(tier.description) {
                                Text(desc).font(Theme.Font.label).foregroundStyle(.secondary)
                            }
                        }
                        .padding(.vertical, Theme.Space.xxs)
                    }
                }
            }

            Section("Purpose Bindings [Model Profiles]") {
                if sortedPurposes.isEmpty {
                    Text("No purpose bindings saved in this profile.")
                        .font(Theme.Font.secondary)
                        .foregroundStyle(.secondary)
                } else {
                    ForEach(sortedPurposes, id: \.id) { id, binding in
                        VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                            Text(ModelProfilesLogic.purposeLabel(id, purposes: purposes))
                                .font(Theme.Font.body)
                            Text(ModelProfilesLogic.purposeSummary(binding, providers: providers))
                                .font(Theme.Font.caption)
                                .foregroundStyle(.secondary)
                        }
                        .padding(.vertical, Theme.Space.xxs)
                    }
                }
            }

            Section("Launcher Overrides [Model Profiles]") {
                if sortedLaunchers.isEmpty {
                    Text("No managed launcher overrides in this profile.")
                        .font(Theme.Font.secondary)
                        .foregroundStyle(.secondary)
                } else {
                    ForEach(sortedLaunchers, id: \.key) { name, models in
                        VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                            Text(ModelProfilesLogic.launcherDisplayName(name, providers: providers))
                                .font(Theme.Font.body)
                            ForEach(models.entries, id: \.field) { entry in
                                let shown = entry.value.isEmpty
                                    ? String(localized: "Clear (use default) [Model Profiles]")
                                    : (entry.field == "effort" ? ModelProfilesLogic.effortLabel(entry.value) : entry.value)
                                Text("\(ModelProfilesLogic.launcherFieldLabel(entry.field)): \(shown)")
                                    .font(Theme.Font.caption)
                                    .foregroundStyle(.secondary)
                            }
                        }
                        .padding(.vertical, Theme.Space.xxs)
                    }
                }
            }
        }
        .themedPage()
        .detailTitle(profile.displayName)
        .navigationBarTitleDisplayMode(.inline)
        .safeAreaInset(edge: .bottom, spacing: Theme.Space.flush) { detailFooter }
    }

    private var detailFooter: some View {
        VStack(spacing: Theme.Space.s) {
            if let error = profiles.displayedError {
                ProfileFooterErrorBanner(
                    error: error,
                    conflict: profiles.conflict,
                    reloadTitle: String(localized: "Reload [Lark Chat Pins View 7bdd5c]"),
                    busy: profiles.busy,
                    errorID: "model-profile-action-error",
                    onReload: onReload
                )
            }
            StackingButtonRow(spacing: Theme.Space.s, stackedSpacing: Theme.Space.s) {
                Button(action: onEdit) {
                    Text("Edit").buttonLabelLine().frame(maxWidth: .infinity)
                }
                .themedButton()
                .disabled(profiles.busy)
                .accessibilityIdentifier("model-profile-edit-button")

                Button(action: onRequestApply) {
                    Group {
                        if profiles.busyAction == .apply {
                            ProgressView()
                        } else {
                            Text("Apply Profile [Model Profiles]").buttonLabelLine()
                        }
                    }
                    .frame(maxWidth: .infinity)
                }
                .themedButton(prominent: true)
                .disabled(profiles.busy)
                .accessibilityIdentifier("model-profile-apply-button")
            }
        }
        .padding(.horizontal, Theme.Space.page)
        .padding(.vertical, Theme.Space.s)
        .background(Theme.groupedPage)
        .overlay(alignment: .top) { Divider() }
        .accessibilityIdentifier("model-profile-detail-footer")
    }
}

/// Pre-apply drift preview screen: names every purpose / launcher field that will change and offers
/// capturing current configuration into the active profile before switching.
struct ModelProfileApplyConfirmView: View {
    var profile: ModelProfile
    var activeProfile: ModelProfile?
    var profiles: ModelProfilesModel
    var purposes: [ModelPurpose]
    var providers: [ModelProvider]
    var onCaptureActiveAndApply: (ModelProfile) -> Void
    var onApplyDirect: () -> Void
    var onCaptureCurrent: () -> Void
    var onReload: () -> Void
    var onCancel: () -> Void

    var body: some View {
        let drift = ModelProfilesLogic.applyDrift(target: profile, active: activeProfile)
        let targetChanges = drift.targetChanges.map {
            ModelProfilesLogic.describeChange($0, profile: profile, purposes: purposes, providers: providers)
        }
        let activeChanges = drift.activeDrift.map {
            ModelProfilesLogic.describeChange($0, profile: nil, purposes: purposes, providers: providers)
        }

        SettingsForm {
            if !targetChanges.isEmpty {
                Section {
                    ForEach(targetChanges) { change in
                        VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                            Text(change.label)
                                .font(Theme.Font.bodyStrong)
                            if let target = change.target {
                                Text(target)
                                    .font(Theme.Font.caption)
                                    .foregroundStyle(.secondary)
                            }
                        }
                        .padding(.vertical, Theme.Space.xxs)
                    }
                } header: {
                    Text(profile.active
                         ? String(localized: "Applying \(profile.displayName) again will overwrite your current live changes for these fields:")
                         : String(localized: "Applying \(profile.displayName) will update these fields in your current configuration:"))
                        .textCase(nil)
                        .font(Theme.Font.secondary)
                }
                .accessibilityIdentifier("model-profile-apply-drift-list")
            } else if let activeProfile, !activeChanges.isEmpty {
                Section {
                    Text("Switching to \(profile.displayName) matches your current settings, but \(activeProfile.displayName) still has unsaved changes:")
                        .font(Theme.Font.secondary)
                        .foregroundStyle(.secondary)
                }
            }

            if drift.canCaptureActiveFirst, let activeProfile {
                if drift.hasActiveDrift {
                    Section {
                        VStack(alignment: .leading, spacing: Theme.Space.s) {
                            Label {
                                Text("Your current configuration differs from the active profile (\(activeProfile.displayName)). Capture it first so switching back does not overwrite these fields:")
                                    .font(Theme.Font.secondary)
                                    .foregroundStyle(Theme.cautionText)
                                    .fixedSize(horizontal: false, vertical: true)
                            } icon: {
                                ThemedIcon(Theme.Icon.error)
                                    .foregroundStyle(Theme.caution)
                            }
                            ForEach(activeChanges) { change in
                                Text(change.label)
                                    .font(Theme.Font.secondaryStrong)
                            }
                        }
                        .padding(.vertical, Theme.Space.xxs)
                        .accessibilityIdentifier("model-profile-active-drift-notice")
                    }
                } else {
                    Section {
                        Text("You can also capture the current configuration into \(activeProfile.displayName) before switching.")
                            .font(Theme.Font.caption)
                            .foregroundStyle(.secondary)
                    }
                }
            }
        }
        .themedPage()
        .detailTitle(String(localized: "Apply \(profile.displayName)? [Model Profiles]"))
        .navigationBarTitleDisplayMode(.inline)
        .navigationBarBackButtonHidden(profiles.busy)
        .safeAreaInset(edge: .bottom, spacing: Theme.Space.flush) { confirmFooter(drift: drift) }
        .accessibilityIdentifier("model-profile-apply-confirm")
    }

    private func confirmFooter(drift: ProfileApplyDrift) -> some View {
        VStack(spacing: Theme.Space.s) {
            if let error = profiles.displayedError {
                ProfileFooterErrorBanner(
                    error: error,
                    conflict: profiles.conflict,
                    reloadTitle: String(localized: "Reload [Lark Chat Pins View 7bdd5c]"),
                    busy: profiles.busy,
                    errorID: "model-profile-action-error",
                    onReload: onReload
                )
            }
            if drift.canCaptureActiveFirst, let activeProfile {
                if drift.hasActiveDrift {
                    captureActiveButton(activeProfile, prominent: true)
                    applyDirectButton(prominent: false)
                } else {
                    applyDirectButton(prominent: true)
                    captureActiveButton(activeProfile, prominent: false)
                }
            } else if profile.active {
                Button(action: onCaptureCurrent) {
                    Group {
                        if profiles.busyAction == .capture {
                            ProgressView()
                        } else {
                            Text("Update from current configuration").buttonLabelLine()
                        }
                    }
                    .frame(maxWidth: .infinity)
                }
                .themedButton(prominent: true)
                .disabled(profiles.busy)
                .accessibilityIdentifier("model-profile-confirm-capture")

                applyDirectButton(prominent: false)
            } else {
                applyDirectButton(prominent: true)
            }

            Button(action: onCancel) {
                Text("Cancel").buttonLabelLine().frame(maxWidth: .infinity)
            }
            .themedButton()
            .disabled(profiles.busy)
            .accessibilityIdentifier("model-profile-confirm-cancel")
        }
        .padding(.horizontal, Theme.Space.page)
        .padding(.vertical, Theme.Space.s)
        .background(Theme.groupedPage)
        .overlay(alignment: .top) { Divider() }
        .accessibilityIdentifier("model-profile-confirm-footer")
    }

    private func captureActiveButton(_ activeProfile: ModelProfile, prominent: Bool) -> some View {
        Button {
            onCaptureActiveAndApply(activeProfile)
        } label: {
            Group {
                if profiles.busyAction == .captureAndApply {
                    ProgressView()
                } else {
                    Text("Capture current into \(activeProfile.displayName) first").buttonLabelLine()
                }
            }
            .frame(maxWidth: .infinity)
        }
        .themedButton(prominent: prominent)
        .disabled(profiles.busy)
        .accessibilityIdentifier("model-profile-capture-and-apply")
    }

    private func applyDirectButton(prominent: Bool) -> some View {
        Button(action: onApplyDirect) {
            Group {
                if profiles.busyAction == .apply {
                    ProgressView()
                } else {
                    Text("Apply directly [Model Profiles]").buttonLabelLine()
                }
            }
            .frame(maxWidth: .infinity)
        }
        .themedButton(prominent: prominent)
        .disabled(profiles.busy)
        .accessibilityIdentifier("model-profile-apply-direct")
    }
}

/// Interactive profile editor (name, description, purpose bindings, Agent presets/tiers) with
/// Update from current configuration and pinned Save bar holding adjacent validation & save errors.
struct ModelProfileEditorView: View {
    var profile: ModelProfile
    var revision: Int64
    var syncKey: Int
    var profiles: ModelProfilesModel
    var purposes: [ModelPurpose]
    var providers: [ModelProvider]
    var onSave: (ModelProfileSaveInput) -> Void
    var onCapture: () -> Void
    var onReload: () -> Void

    @State private var draft = ModelProfileDraft()
    @State private var initialDraft = ModelProfileDraft()
    @State private var syncedToken = ""
    @State private var tierCounter = 0

    private var currentToken: String { "\(profile.id):\(revision):\(syncKey)" }

    var body: some View {
        let problems = ModelProfilesLogic.draftProblems(draft)
        let dirty = ModelProfilesLogic.isDraftDirty(draft, original: initialDraft)
        let canSave = !profiles.busy && !profiles.conflict && problems.isEmpty && dirty
        let hubSupportsPermissionMode = ModelProfileRules.hubSupportsPermissionMode(purposes: purposes)
        let launcherProviders = providers.filter { $0.kind == "launcher" && $0.id.hasPrefix("launcher.") && $0.hidden != true }

        SettingsForm {
            Section {
                VStack(alignment: .leading, spacing: Theme.Space.s) {
                    Text(profile.active
                         ? String(localized: "Saving updates this active profile. Agent presets apply on the next Agent start; purpose and launcher changes take effect when you apply this profile again. Credentials are never shown or modified here.")
                         : String(localized: "Saving updates this profile template without changing your active models. Apply the profile afterwards to switch. Credentials are never shown or modified here."))
                        .font(Theme.Font.caption)
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                    Button(action: onCapture) {
                        Group {
                            if profiles.busyAction == .capture {
                                ProgressView()
                            } else {
                                Text("Update from current configuration").buttonLabelLine()
                            }
                        }
                        .frame(maxWidth: .infinity)
                    }
                    .themedButton()
                    .disabled(profiles.busy)
                    .accessibilityIdentifier("model-profile-editor-capture-button")
                }
                .padding(.vertical, Theme.Space.xxs)
            }

            Section("Profile Details [Model Profiles]") {
                TextField(String(localized: "Name"), text: $draft.name)
                    .disabled(profiles.busy)
                    .accessibilityIdentifier("model-profile-name-field")
                TextField(String(localized: "Description [Model Profiles]"), text: $draft.description)
                    .disabled(profiles.busy)
                    .accessibilityIdentifier("model-profile-description-field")
            }

            Section {
                ForEach(draft.purposeOrder, id: \.self) { purposeID in
                    purposeSectionRows(purposeID: purposeID, hubSupportsPermissionMode: hubSupportsPermissionMode)
                }
            } header: {
                Text("Purpose Bindings [Model Profiles]")
            } footer: {
                Text("Enabled categories are written when switching to this profile; disabled ones stay untouched.")
                    .font(Theme.Font.caption)
            }
            .disabled(profiles.busy)

            Section {
                if draft.tiers.isEmpty {
                    Text("No Agent presets defined in this profile.")
                        .font(Theme.Font.secondary)
                        .foregroundStyle(.secondary)
                } else {
                    ForEach($draft.tiers) { $tier in
                        tierEditorGroup(tier: $tier, launcherProviders: launcherProviders,
                                        hubSupportsPermissionMode: hubSupportsPermissionMode)
                    }
                }
                Button {
                    guard draft.tiers.count < ModelProfileRules.maxTiers else { return }
                    tierCounter += 1
                    let defaultLauncher = launcherProviders.first.map { String($0.id.dropFirst("launcher.".count)) } ?? "claude"
                    draft.tiers.append(ModelProfileTierDraft(id: "new-tier-\(tierCounter)", launcher: defaultLauncher))
                } label: {
                    Label("Add Agent Preset [Model Profiles]", systemImage: Theme.Icon.add).lineLimit(1)
                }
                .disabled(profiles.busy || draft.tiers.count >= ModelProfileRules.maxTiers)
                .accessibilityIdentifier("model-profile-add-tier")
            } header: {
                Text("Agent Presets (\(draft.tiers.count)/\(ModelProfileRules.maxTiers)) [Model Profiles]")
            } footer: {
                Text("Named launch presets for Agents. Only the active profile's presets are used.")
                    .font(Theme.Font.caption)
            }
            .disabled(profiles.busy)
        }
        .themedPage()
        .detailTitle(String(localized: "Edit \(profile.displayName) [Model Profiles]"))
        .navigationBarTitleDisplayMode(.inline)
        .navigationBarBackButtonHidden(profiles.busy)
        .safeAreaInset(edge: .bottom, spacing: Theme.Space.flush) {
            saveBar(problems: problems, canSave: canSave)
        }
        .task(id: currentToken) { syncDraftIfNeeded() }
        .accessibilityIdentifier("model-profile-editor")
    }

    private func syncDraftIfNeeded() {
        guard syncedToken != currentToken else { return }
        let loaded = ModelProfilesLogic.profileToDraft(profile, catalogPurposeIDs: purposes.map(\.id))
        initialDraft = loaded
        draft = loaded
        syncedToken = currentToken
    }

    @ViewBuilder
    private func purposeSectionRows(purposeID: String, hubSupportsPermissionMode: Bool) -> some View {
        let entry = draft.purposes[purposeID] ?? ModelProfilePurposeDraft()
        let catalogPurpose = purposes.first { $0.id == purposeID }
        let showPermissionMode = ModelProfileRules.showsPurposePermissionMode(
            purposeID: purposeID,
            currentValue: entry.permissionMode,
            catalogPurpose: catalogPurpose,
            hubSupportsPermissionMode: hubSupportsPermissionMode
        )

        Toggle(isOn: Binding(
            get: { draft.purposes[purposeID]?.included ?? false },
            set: { included in updatePurpose(purposeID) { $0.included = included } }
        )) {
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                Text(ModelProfilesLogic.purposeLabel(purposeID, purposes: purposes))
                    .font(Theme.Font.body)
                Text(entry.included
                     ? String(localized: "Updated on switch [Model Profiles]")
                     : String(localized: "Left unchanged [Model Profiles]"))
                    .font(Theme.Font.caption)
                    .foregroundStyle(.secondary)
            }
        }
        .accessibilityIdentifier("model-profile-purpose-toggle-\(purposeID)")

        if entry.included {
            let candidates = ModelProfilesLogic.candidateProviders(
                for: purposeID,
                purposes: purposes,
                providers: providers,
                currentProvider: entry.provider
            )
            Picker("Model Providers", selection: Binding(
                get: { draft.purposes[purposeID]?.provider ?? "" },
                set: { next in
                    updatePurpose(purposeID) {
                        guard $0.provider != next else { return }
                        $0.provider = next
                        $0.model = ""
                        $0.effort = ""
                        $0.permissionMode = ""
                    }
                }
            )) {
                Text("Follow default [Model Profiles]").tag("")
                ForEach(candidates) { provider in
                    Text(provider.name).tag(provider.id)
                }
                if !entry.provider.isEmpty && !candidates.contains(where: { $0.id == entry.provider }) {
                    Text(entry.provider).tag(entry.provider)
                }
            }
            .lineLimit(1)

            TextField(
                String(localized: "Model (leave empty for default)"),
                text: Binding(
                    get: { draft.purposes[purposeID]?.model ?? "" },
                    set: { next in updatePurpose(purposeID) { $0.model = next } }
                )
            )
            .font(Theme.Font.body.monospaced())
            .textInputAutocapitalization(.never)
            .autocorrectionDisabled()

            Picker("Thinking Effort", selection: Binding(
                get: { draft.purposes[purposeID]?.effort ?? "" },
                set: { next in updatePurpose(purposeID) { $0.effort = next } }
            )) {
                Text("Follow default [Model Profiles]").tag("")
                ForEach(ModelProfileRules.effortLevels, id: \.self) { level in
                    Text(ModelProfilesLogic.effortLabel(level)).tag(level)
                }
            }
            .lineLimit(1)

            if showPermissionMode {
                Picker("Permission Mode", selection: Binding(
                    get: { draft.purposes[purposeID]?.permissionMode ?? "" },
                    set: { next in updatePurpose(purposeID) { $0.permissionMode = next } }
                )) {
                    Text("Follow default [Model Profiles]").tag("")
                    ForEach(ModelProfileRules.permissionModes, id: \.self) { mode in
                        Text(ModelProfilesLogic.permissionModeLabel(mode)).tag(mode)
                    }
                }
                .lineLimit(1)
                .accessibilityIdentifier("purpose-permission-mode-\(purposeID)")
            }
        }
    }

    @ViewBuilder
    private func tierEditorGroup(tier: Binding<ModelProfileTierDraft>, launcherProviders: [ModelProvider],
                                 hubSupportsPermissionMode: Bool) -> some View {
        let current = tier.wrappedValue
        let showPermissionMode = ModelProfileRules.showsTierPermissionMode(
            currentValue: current.permissionMode,
            hubSupportsPermissionMode: hubSupportsPermissionMode
        )
        let launcherOptions = tierLauncherOptions(launcherProviders: launcherProviders, current: current.launcher)

        VStack(alignment: .leading, spacing: Theme.Space.s) {
            TextField(String(localized: "Preset name (e.g. strong, fast) [Model Profiles]"), text: tier.name)
                .font(Theme.Font.body.monospaced())
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
                .accessibilityIdentifier("tier-name-\(current.id)")

            Picker("Launcher [Model Profiles]", selection: Binding(
                get: { tier.wrappedValue.launcher },
                set: { next in
                    guard tier.wrappedValue.launcher != next else { return }
                    tier.wrappedValue.launcher = next
                    tier.wrappedValue.model = ""
                    tier.wrappedValue.effort = ""
                    tier.wrappedValue.permissionMode = ""
                }
            )) {
                ForEach(launcherOptions, id: \.id) { option in
                    Text(option.title).tag(option.id)
                }
            }
            .lineLimit(1)

            TextField(String(localized: "Model (leave empty for default)"), text: tier.model)
                .font(Theme.Font.body.monospaced())
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()

            Picker("Thinking Effort", selection: tier.effort) {
                Text("Follow default [Model Profiles]").tag("")
                ForEach(ModelProfileRules.effortLevels, id: \.self) { level in
                    Text(ModelProfilesLogic.effortLabel(level)).tag(level)
                }
            }
            .lineLimit(1)

            Picker("Speed [Model Profiles]", selection: Binding(
                get: { tier.wrappedValue.speed == "standard" ? "" : tier.wrappedValue.speed },
                set: { tier.wrappedValue.speed = $0 }
            )) {
                ForEach(ModelProfileRules.tierSpeeds, id: \.self) { speed in
                    Text(ModelProfilesLogic.tierSpeedLabel(speed)).tag(speed)
                }
            }
            .lineLimit(1)

            if showPermissionMode {
                Picker("Permission Mode", selection: tier.permissionMode) {
                    Text("Follow default [Model Profiles]").tag("")
                    ForEach(ModelProfileRules.permissionModes, id: \.self) { mode in
                        Text(ModelProfilesLogic.permissionModeLabel(mode)).tag(mode)
                    }
                }
                .lineLimit(1)
                .accessibilityIdentifier("tier-permission-mode-\(current.id)")
            }

            TextField(String(localized: "When to use this preset [Model Profiles]"), text: tier.description)

            Button(role: .destructive) {
                draft.tiers.removeAll { $0.id == current.id }
            } label: {
                Label("Remove Preset [Model Profiles]", systemImage: Theme.Icon.delete).lineLimit(1)
            }
            .font(Theme.Font.label)
        }
        .padding(.vertical, Theme.Space.xs)
    }

    private func tierLauncherOptions(launcherProviders: [ModelProvider], current: String) -> [(id: String, title: String)] {
        var list = launcherProviders.map { (id: String($0.id.dropFirst("launcher.".count)), title: $0.name) }
        if !list.contains(where: { $0.id == "claude" }) {
            list.append((id: "claude", title: "Claude Code"))
        }
        let trimmed = current.trimmingCharacters(in: .whitespacesAndNewlines)
        if !trimmed.isEmpty && !list.contains(where: { $0.id == trimmed }) {
            list.append((id: trimmed, title: trimmed))
        }
        return list
    }

    private func saveBar(problems: [String], canSave: Bool) -> some View {
        VStack(spacing: Theme.Space.s) {
            if !problems.isEmpty {
                ScrollView {
                    VStack(alignment: .leading, spacing: Theme.Space.xs) {
                        ForEach(problems, id: \.self) { problem in
                            HStack(alignment: .firstTextBaseline, spacing: Theme.Space.s) {
                                ThemedIcon(Theme.Icon.error)
                                    .font(Theme.Font.labelStrong)
                                    .foregroundStyle(Theme.danger)
                                Text(problem)
                                    .font(Theme.Font.label)
                                    .foregroundStyle(Theme.danger)
                                    .fixedSize(horizontal: false, vertical: true)
                            }
                        }
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                }
                .frame(maxHeight: Theme.Size.box120)
                .accessibilityIdentifier("model-profile-editor-problems")
            }

            if let error = profiles.displayedError {
                ProfileFooterErrorBanner(
                    error: error,
                    conflict: profiles.conflict,
                    reloadTitle: String(localized: "Reload (discard edits) [Model Profiles]"),
                    busy: profiles.busy,
                    errorID: "model-profile-save-error",
                    onReload: onReload
                )
            }

            Button {
                guard canSave else { return }
                onSave(ModelProfilesLogic.draftToSaveInput(draft, revision: revision))
            } label: {
                Group {
                    if profiles.busyAction == .save {
                        ProgressView()
                    } else {
                        Text("Save").buttonLabelLine()
                    }
                }
                .frame(maxWidth: .infinity)
            }
            .themedButton(prominent: true)
            .disabled(!canSave)
            .accessibilityIdentifier("model-profile-save-button")
        }
        .padding(.horizontal, Theme.Space.page)
        .padding(.vertical, Theme.Space.s)
        .background(Theme.groupedPage)
        .overlay(alignment: .top) { Divider() }
        .accessibilityIdentifier("model-profile-editor-save-bar")
    }

    private func updatePurpose(_ id: String, _ mutate: (inout ModelProfilePurposeDraft) -> Void) {
        var entry = draft.purposes[id] ?? ModelProfilePurposeDraft()
        mutate(&entry)
        draft.purposes[id] = entry
    }
}
