import Foundation
import Observation
import PersonaWire

nonisolated enum ModelProfileRules {
    static let categoryIDs = ["assistant", "agent.new", "kb", "inbox.triage", "agent.delegate", "translate"]
    static let launcherModelFields = [
        "model", "small_model", "subagent_model", "opus_model", "sonnet_model", "haiku_model", "fable_model", "effort",
    ]
    static let effortLevels = ["low", "medium", "high", "xhigh", "max"]
    static let tierSpeeds = ["", "fast", "ultrafast"]
    static let validSpeeds: Set<String> = ["", "standard", "fast", "ultrafast"]
    static let permissionModes = ["default", "acceptEdits", "plan", "auto", "bypassPermissions"]
    static let validPermissionModes: Set<String> = Set(permissionModes)

    static let maxTiers = 12
    static let maxName = 40
    static let maxDescription = 300
    static let maxTierDescription = 100
    static let maxValueBytes = 200

    /// Matches `^[a-z][a-z0-9_-]{0,23}$` without runtime regex compilation.
    static func isValidTierName(_ name: String) -> Bool {
        let bytes = Array(name.utf8)
        guard !bytes.isEmpty, bytes.count <= 24 else { return false }
        guard bytes[0] >= UInt8(ascii: "a"), bytes[0] <= UInt8(ascii: "z") else { return false }
        return bytes.dropFirst().allSatisfy { b in
            (b >= UInt8(ascii: "a") && b <= UInt8(ascii: "z"))
                || (b >= UInt8(ascii: "0") && b <= UInt8(ascii: "9"))
                || b == UInt8(ascii: "_") || b == UInt8(ascii: "-")
        }
    }

    static func isValidValue(_ value: String) -> Bool {
        value == value.trimmingCharacters(in: .whitespacesAndNewlines)
            && !value.contains(where: { $0 == "\r" || $0 == "\n" || $0 == "\t" || $0 == "\0" })
            && value.utf8.count <= maxValueBytes
    }

    /// Older hubs omit `permission_mode` from launcher purposes' `fields` capability list.
    static func hubSupportsPermissionMode(purposes: [ModelPurpose]) -> Bool {
        let launcherPurposes = purposes.filter { $0.kind == "launcher" && $0.fields != nil }
        return launcherPurposes.isEmpty || launcherPurposes.contains { $0.supports("permission_mode") }
    }

    static func showsPurposePermissionMode(purposeID: String, currentValue: String, catalogPurpose: ModelPurpose?,
                                           hubSupportsPermissionMode: Bool) -> Bool {
        let isLauncher = catalogPurpose?.kind == "launcher" || purposeID == "assistant" || purposeID == "agent.new"
        guard isLauncher else { return false }
        if !currentValue.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty { return true }
        if let catalogPurpose, catalogPurpose.fields != nil { return catalogPurpose.supports("permission_mode") }
        return hubSupportsPermissionMode
    }

    static func showsTierPermissionMode(currentValue: String, hubSupportsPermissionMode: Bool) -> Bool {
        hubSupportsPermissionMode || !currentValue.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }
}

nonisolated enum ParsedProfileChange: Equatable, Sendable {
    case purpose(id: String)
    case launcher(launcher: String, field: String)
    case other(raw: String)
}

nonisolated struct ProfileApplyDrift: Equatable, Sendable {
    var targetChanges: [String]
    var activeDrift: [String]
    var hasDrift: Bool
    var hasActiveDrift: Bool
    var canCaptureActiveFirst: Bool
}

nonisolated struct DescribedProfileChange: Identifiable, Equatable, Sendable {
    var id: String
    var label: String
    var target: String? = nil
}

nonisolated struct ModelProfilePurposeDraft: Equatable, Sendable {
    var included = false
    var provider = ""
    var model = ""
    var effort = ""
    var permissionMode = ""
}

nonisolated struct ModelProfileTierDraft: Identifiable, Equatable, Sendable {
    var id: String
    var originalName: String? = nil
    var originalModel: String? = nil
    var name = ""
    var launcher = ""
    var model = ""
    var effort = ""
    var permissionMode = ""
    var speed = ""
    var description = ""
}

nonisolated struct ModelProfileDraft: Equatable, Sendable {
    var name = ""
    var description = ""
    var purposeOrder: [String] = []
    var purposes: [String: ModelProfilePurposeDraft] = [:]
    var launchers: [String: ModelProfileLauncherModels] = [:]
    var tiers: [ModelProfileTierDraft] = []
}

nonisolated enum ModelProfilesLogic {
    /// Parses a `changes` entry from `GET /api/v1/model-profiles` into a purpose ID or a `<launcher>.<field>` override.
    static func parseChange(_ change: String, knownPurposeIDs: [String] = []) -> ParsedProfileChange {
        if ModelProfileRules.categoryIDs.contains(change) || knownPurposeIDs.contains(change) {
            return .purpose(id: change)
        }
        if let dot = change.lastIndex(of: "."), dot > change.startIndex {
            let field = String(change[change.index(after: dot)...])
            if ModelProfileRules.launcherModelFields.contains(field) {
                return .launcher(launcher: String(change[..<dot]), field: field)
            }
        }
        return .other(raw: change)
    }

    /// Summarizes live-configuration drift relevant before applying `target`.
    static func applyDrift(target: ModelProfile, active: ModelProfile?) -> ProfileApplyDrift {
        let targetChanges = target.changes
        let switchingFromOther = active != nil && active?.id != target.id
        let activeDrift = switchingFromOther ? (active?.changes ?? []) : []
        let hasActiveDrift = !activeDrift.isEmpty
        return ProfileApplyDrift(
            targetChanges: targetChanges,
            activeDrift: activeDrift,
            hasDrift: !targetChanges.isEmpty || hasActiveDrift,
            hasActiveDrift: hasActiveDrift,
            canCaptureActiveFirst: switchingFromOther
        )
    }

    static func shouldConfirmApply(target: ModelProfile, active: ModelProfile?) -> Bool {
        applyDrift(target: target, active: active).hasDrift
    }

    static func categoryIDs(for profile: ModelProfile, catalogPurposeIDs: [String] = []) -> [String] {
        var ordered = ModelProfileRules.categoryIDs
        for id in catalogPurposeIDs where ModelProfileRules.categoryIDs.contains(id) && !ordered.contains(id) {
            ordered.append(id)
        }
        for id in profile.purposes.keys.sorted() where !ordered.contains(id) {
            ordered.append(id)
        }
        return ordered
    }

    static func sortedPurposes(of profile: ModelProfile) -> [(id: String, binding: ModelBinding)] {
        profile.purposes.sorted { lhs, rhs in
            let li = ModelProfileRules.categoryIDs.firstIndex(of: lhs.key) ?? Int.max
            let ri = ModelProfileRules.categoryIDs.firstIndex(of: rhs.key) ?? Int.max
            return li != ri ? li < ri : lhs.key < rhs.key
        }.map { ($0.key, $0.value) }
    }

    static func profileToDraft(_ profile: ModelProfile, catalogPurposeIDs: [String] = []) -> ModelProfileDraft {
        let order = categoryIDs(for: profile, catalogPurposeIDs: catalogPurposeIDs)
        var purposes: [String: ModelProfilePurposeDraft] = [:]
        for id in order {
            if let binding = profile.purposes[id] {
                purposes[id] = ModelProfilePurposeDraft(
                    included: true,
                    provider: binding.provider ?? "",
                    model: binding.model ?? "",
                    effort: binding.effort ?? "",
                    permissionMode: binding.permissionMode ?? ""
                )
            } else {
                purposes[id] = ModelProfilePurposeDraft(included: false)
            }
        }
        let tiers = profile.tiers.sorted(by: { $0.key < $1.key }).enumerated().map { index, pair in
            let (tierName, tier) = pair
            return ModelProfileTierDraft(
                id: "tier-\(index)-\(tierName)",
                originalName: tierName,
                originalModel: tier.model ?? "",
                name: tierName,
                launcher: tier.launcher,
                model: tier.model ?? "",
                effort: tier.effort ?? "",
                permissionMode: tier.permissionMode ?? "",
                speed: tier.speed == "standard" ? "" : (tier.speed ?? ""),
                description: tier.description ?? ""
            )
        }
        return ModelProfileDraft(
            name: profile.name,
            description: profile.description ?? "",
            purposeOrder: order,
            purposes: purposes,
            launchers: profile.launchers,
            tiers: tiers
        )
    }

    static func draftProblems(_ draft: ModelProfileDraft) -> [String] {
        var problems: [String] = []
        let name = draft.name.trimmingCharacters(in: .whitespacesAndNewlines)
        if name.isEmpty || name.unicodeScalars.count > ModelProfileRules.maxName
            || name.contains(where: { $0 == "\r" || $0 == "\n" || $0 == "\0" }) {
            problems.append(String(localized: "Profile name is required and must be at most \(ModelProfileRules.maxName) single-line characters."))
        }
        let desc = draft.description.trimmingCharacters(in: .whitespacesAndNewlines)
        if desc.unicodeScalars.count > ModelProfileRules.maxDescription || desc.contains("\0") {
            problems.append(String(localized: "Description must be at most \(ModelProfileRules.maxDescription) characters."))
        }
        for id in draft.purposeOrder {
            guard let entry = draft.purposes[id], entry.included else { continue }
            for (field, raw) in [("provider", entry.provider), ("model", entry.model)] {
                let value = raw.trimmingCharacters(in: .whitespacesAndNewlines)
                if !value.isEmpty && !ModelProfileRules.isValidValue(value) {
                    problems.append(String(localized: "\(id) \(field) is invalid or too long."))
                }
            }
            let effort = entry.effort.trimmingCharacters(in: .whitespacesAndNewlines)
            if !effort.isEmpty && !ModelProfileRules.effortLevels.contains(effort) {
                problems.append(String(localized: "\(id) thinking effort “\(effort)” is not supported."))
            }
            let mode = entry.permissionMode.trimmingCharacters(in: .whitespacesAndNewlines)
            if !mode.isEmpty && (!ModelProfileRules.isValidValue(mode) || !ModelProfileRules.validPermissionModes.contains(mode)) {
                problems.append(String(localized: "\(id) permission mode is invalid."))
            }
        }
        if draft.tiers.count > ModelProfileRules.maxTiers {
            problems.append(String(localized: "At most \(ModelProfileRules.maxTiers) Agent presets are allowed."))
        }
        var seenTiers: Set<String> = []
        for tier in draft.tiers {
            let tierName = tier.name.trimmingCharacters(in: .whitespacesAndNewlines)
            let label = tierName.isEmpty ? "?" : tierName
            if !ModelProfileRules.isValidTierName(tierName) {
                problems.append(String(localized: "Preset name “\(label)” must start with a lowercase letter and use only a-z, 0-9, _ or - (max 24 chars)."))
            } else if !seenTiers.insert(tierName).inserted {
                problems.append(String(localized: "Duplicate preset name “\(tierName)”."))
            }
            let launcher = tier.launcher.trimmingCharacters(in: .whitespacesAndNewlines)
            if launcher.isEmpty {
                problems.append(String(localized: "Preset “\(label)” needs a launcher."))
            } else if !ModelProfileRules.isValidValue(launcher) || launcher.contains(" ") {
                problems.append(String(localized: "\(label) launcher is invalid or too long."))
            }
            let model = tier.model.trimmingCharacters(in: .whitespacesAndNewlines)
            if !model.isEmpty {
                let unchangedLegacy = tier.originalName == tierName && tier.originalModel == model
                if !ModelProfileRules.isValidValue(model) || model.contains(" ") || (!unchangedLegacy && !ModelID.isValid(model)) {
                    problems.append(String(localized: "Preset “\(label)” has an invalid model ID “\(model)”."))
                }
            }
            let effort = tier.effort.trimmingCharacters(in: .whitespacesAndNewlines)
            if !effort.isEmpty && !ModelProfileRules.effortLevels.contains(effort) {
                problems.append(String(localized: "\(label) thinking effort “\(effort)” is not supported."))
            }
            let mode = tier.permissionMode.trimmingCharacters(in: .whitespacesAndNewlines)
            if !mode.isEmpty && (!ModelProfileRules.isValidValue(mode) || !ModelProfileRules.validPermissionModes.contains(mode)) {
                problems.append(String(localized: "\(label) permission mode is invalid."))
            }
            let speed = tier.speed.trimmingCharacters(in: .whitespacesAndNewlines)
            if !ModelProfileRules.validSpeeds.contains(speed) {
                problems.append(String(localized: "Preset “\(label)” has an unsupported speed “\(speed)”."))
            }
            let tierDesc = tier.description.trimmingCharacters(in: .whitespacesAndNewlines)
            if tierDesc.unicodeScalars.count > ModelProfileRules.maxTierDescription
                || tierDesc.contains(where: { $0 == "\r" || $0 == "\n" || $0 == "\0" }) {
                problems.append(String(localized: "Preset “\(label)” description must be one line of at most \(ModelProfileRules.maxTierDescription) characters."))
            }
        }
        return problems
    }

    static func draftToSaveInput(_ draft: ModelProfileDraft, revision: Int64) -> ModelProfileSaveInput {
        var purposes: [String: ModelBinding] = [:]
        for id in draft.purposeOrder {
            guard let entry = draft.purposes[id], entry.included else { continue }
            purposes[id] = ModelBinding(
                provider: trim(entry.provider),
                model: trim(entry.model),
                effort: trim(entry.effort),
                runtime: nil,
                permissionMode: trim(entry.permissionMode)
            )
        }
        let launchers = draft.launchers.filter { !$0.value.isEmpty }
        var tiers: [String: ModelProfileTier] = [:]
        for entry in draft.tiers {
            guard let tierName = trim(entry.name) else { continue }
            let speed = trim(entry.speed)
            tiers[tierName] = ModelProfileTier(
                launcher: entry.launcher.trimmingCharacters(in: .whitespacesAndNewlines),
                model: trim(entry.model),
                effort: trim(entry.effort),
                // Omit default/standard speed so older hubs using strict JSON decoding accept saves cleanly.
                speed: speed == "standard" ? nil : speed,
                permissionMode: trim(entry.permissionMode),
                description: trim(entry.description)
            )
        }
        return ModelProfileSaveInput(
            revision: revision,
            name: draft.name.trimmingCharacters(in: .whitespacesAndNewlines),
            description: draft.description.trimmingCharacters(in: .whitespacesAndNewlines),
            purposes: purposes.isEmpty ? nil : purposes,
            launchers: launchers.isEmpty ? nil : launchers,
            tiers: tiers.isEmpty ? nil : tiers
        )
    }

    static func isDraftDirty(_ draft: ModelProfileDraft, original: ModelProfileDraft) -> Bool {
        draftToSaveInput(draft, revision: 0) != draftToSaveInput(original, revision: 0)
    }

    static func purposeLabel(_ id: String, purposes: [ModelPurpose] = []) -> String {
        if let name = purposes.first(where: { $0.id == id })?.name.trimmingCharacters(in: .whitespacesAndNewlines), !name.isEmpty {
            return name
        }
        switch id {
        case "assistant": return String(localized: "Main Chat")
        case "agent.new": return String(localized: "Agents")
        case "kb": return String(localized: "Information Processing [Model Profiles]")
        case "inbox.triage": return String(localized: "Inbox Screening [Model Profiles]")
        case "agent.delegate": return String(localized: "Answering Agents' Questions [Model Profiles]")
        case "translate": return String(localized: "Translation")
        default: return id
        }
    }

    static func effortLabel(_ effort: String) -> String {
        SessionModes.effortLabel(effort) ?? effort
    }

    static func permissionModeLabel(_ mode: String) -> String {
        SessionModes.mode(mode)?.title ?? SessionModes.listedOption(mode).title
    }

    static func tierSpeedLabel(_ speed: String) -> String {
        switch speed {
        case "fast": String(localized: "Fast")
        case "ultrafast": String(localized: "Ultra Fast [Model Profiles]")
        default: String(localized: "Standard")
        }
    }

    static func launcherFieldLabel(_ field: String) -> String {
        switch field {
        case "model": String(localized: "Default Model [Model Profiles]")
        case "small_model": String(localized: "Small Model [Model Profiles]")
        case "subagent_model": String(localized: "Subagent Model [Model Profiles]")
        case "opus_model": String(localized: "Opus Alias [Model Profiles]")
        case "sonnet_model": String(localized: "Sonnet Alias [Model Profiles]")
        case "haiku_model": String(localized: "Haiku Alias [Model Profiles]")
        case "fable_model": String(localized: "Fable Alias [Model Profiles]")
        case "effort": String(localized: "Thinking Effort")
        default: field
        }
    }

    static func providerDisplayName(_ providerID: String, providers: [ModelProvider]) -> String {
        providers.first(where: { $0.id == providerID })?.name ?? providerID
    }

    static func launcherDisplayName(_ launcher: String, providers: [ModelProvider]) -> String {
        providers.first(where: { $0.id == "launcher.\(launcher)" })?.name ?? launcher
    }

    static func purposeSummary(_ binding: ModelBinding, providers: [ModelProvider]) -> String {
        let providerLabel = trim(binding.provider).map { providerDisplayName($0, providers: providers) }
            ?? String(localized: "Follow default [Model Profiles]")
        return [
            providerLabel,
            trim(binding.model),
            trim(binding.effort).map(effortLabel),
            trim(binding.permissionMode).map(permissionModeLabel),
        ].compactMap { $0 }.joined(separator: " · ")
    }

    static func tierRunsSummary(_ tier: ModelProfileTier, providers: [ModelProvider]) -> String {
        let speedLabel = trim(tier.speed).flatMap { ($0 == "fast" || $0 == "ultrafast") ? tierSpeedLabel($0) : nil }
        return [
            trim(tier.launcher).map { launcherDisplayName($0, providers: providers) },
            trim(tier.model),
            trim(tier.effort).map(effortLabel),
            trim(tier.permissionMode).map(permissionModeLabel),
            speedLabel,
        ].compactMap { $0 }.joined(separator: " · ")
    }

    static func describeChange(_ change: String, profile: ModelProfile?, purposes: [ModelPurpose] = [],
                               providers: [ModelProvider] = []) -> DescribedProfileChange {
        switch parseChange(change, knownPurposeIDs: purposes.map(\.id)) {
        case .purpose(let id):
            let label = purposeLabel(id, purposes: purposes)
            let target = profile?.purposes[id].map { purposeSummary($0, providers: providers) }
            return DescribedProfileChange(id: change, label: label, target: target)
        case .launcher(let launcher, let field):
            let label = "\(launcherDisplayName(launcher, providers: providers)) · \(launcherFieldLabel(field))"
            let raw = profile?.launchers[launcher]?.value(for: field)
            let target: String? = if let raw {
                if raw.isEmpty {
                    String(localized: "Clear (use default) [Model Profiles]")
                } else if field == "effort" {
                    effortLabel(raw)
                } else {
                    raw
                }
            } else {
                nil
            }
            return DescribedProfileChange(id: change, label: label, target: target)
        case .other(let raw):
            return DescribedProfileChange(id: change, label: raw, target: nil)
        }
    }

    static func candidateProviders(for purposeID: String, purposes: [ModelPurpose], providers: [ModelProvider],
                                   currentProvider: String) -> [ModelProvider] {
        let purpose = purposes.first { $0.id == purposeID }
        return providers.filter { provider in
            if provider.hidden == true && provider.id != currentProvider { return false }
            if let ids = purpose?.providerIDs { return ids.contains(provider.id) || provider.id == currentProvider }
            if let kind = purpose?.kind { return provider.kind == kind || provider.id == currentProvider }
            return true
        }
    }

    static func trim(_ value: String?) -> String? {
        guard let trimmed = value?.trimmingCharacters(in: .whitespacesAndNewlines), !trimmed.isEmpty else { return nil }
        return trimmed
    }
}

/// Manages `GET/PUT /api/v1/model-profiles`, apply, rollback, live capture (`capture: true`), and
/// capture-active-before-apply flows for Settings › Models.
@Observable final class ModelProfilesModel {
    enum BusyAction: Equatable, Sendable {
        case apply, rollback, save, capture, captureAndApply
    }

    enum CaptureAndApplyResult: Equatable, Sendable {
        case applied, captureFailed, applyFailedAfterCapture
    }

    struct Deps {
        var api: () -> APIClient?
        var updated: () async -> Void = {}

        static var live: Deps {
            let app = AppModel.shared
            let scope = AssistantSettingsRequestScope(app)
            return .init(api: { scope.api(app) }, updated: { app.launcherDefaultsChanged(); await app.refreshAssistant() })
        }
    }

    private(set) var catalog: ModelProfilesCatalog? = nil
    private(set) var loading = false
    private(set) var loaded = false
    private(set) var unsupported = false
    private(set) var busyAction: BusyAction? = nil
    private(set) var error: String? = nil
    private(set) var actionError: String? = nil
    private(set) var conflict = false
    /// Bumped whenever the server catalog reloads, captures, or saves so open editors resync their draft cleanly.
    private(set) var syncKey = 0

    @ObservationIgnored private let deps: Deps
    @ObservationIgnored private(set) var requestAPI: APIClient? = nil
    @ObservationIgnored var owner: ModelSettingsOwner = .current

    init(deps: Deps = .live) {
        self.deps = deps
    }

    var busy: Bool { busyAction != nil }
    var displayedError: String? { actionError ?? error }

    var activeProfile: ModelProfile? {
        guard let activeID = catalog?.active, !activeID.isEmpty else { return nil }
        return catalog?.profiles.first { $0.id == activeID }
    }

    func profile(id: String) -> ModelProfile? {
        catalog?.profiles.first { $0.id == id }
    }

    func clearErrors() {
        error = nil
        actionError = nil
        conflict = false
    }

    @discardableResult
    func load() async -> ModelProfilesCatalog? {
        guard !loading else { return catalog }
        loading = true
        defer { loading = false; loaded = true }
        do {
            let api = try resolveAPI()
            if let result = try await api.modelProfiles() {
                catalog = result
                unsupported = false
                error = nil
                conflict = false
                syncKey += 1
                return result
            } else {
                catalog = nil
                unsupported = true
                error = nil
                conflict = false
                return nil
            }
        } catch {
            self.error = Self.errorMessage(error)
            return nil
        }
    }

    func apply(id: String) async -> Bool {
        guard !busy else { return false }
        busyAction = .apply
        actionError = nil
        conflict = false
        defer { busyAction = nil }
        do {
            let api = try resolveAPI()
            let updated = try await api.applyModelProfile(id)
            catalog = updated
            unsupported = false
            error = nil
            actionError = nil
            conflict = false
            await deps.updated()
            return true
        } catch {
            actionError = Self.errorMessage(error)
            return false
        }
    }

    func rollback() async -> Bool {
        guard !busy else { return false }
        busyAction = .rollback
        actionError = nil
        conflict = false
        defer { busyAction = nil }
        do {
            let api = try resolveAPI()
            let updated = try await api.rollbackModelProfile()
            catalog = updated
            unsupported = false
            error = nil
            actionError = nil
            conflict = false
            await deps.updated()
            return true
        } catch {
            actionError = Self.errorMessage(error)
            return false
        }
    }

    func save(id: String, input: ModelProfileSaveInput) async -> Bool {
        guard !busy else { return false }
        busyAction = .save
        actionError = nil
        conflict = false
        defer { busyAction = nil }
        do {
            let api = try resolveAPI()
            switch try await api.saveModelProfile(id, input: input) {
            case .saved(let updated):
                catalog = updated
                unsupported = false
                error = nil
                actionError = nil
                conflict = false
                syncKey += 1
                await deps.updated()
                return true
            case .conflict:
                conflict = true
                actionError = String(localized: "Profiles were changed elsewhere. Your edits are kept; reload to review the latest version before saving.")
                return false
            }
        } catch {
            actionError = Self.errorMessage(error)
            return false
        }
    }

    /// Captures the hub's current live model configuration into `profile` via `PUT /api/v1/model-profiles/{id}` with
    /// `capture: true`, preserving name, description, and existing Agent presets.
    func capture(_ profile: ModelProfile, revision: Int64? = nil) async -> Bool {
        guard !busy else { return false }
        busyAction = .capture
        actionError = nil
        conflict = false
        defer { busyAction = nil }
        let rev = revision ?? catalog?.revision ?? 0
        let input = ModelProfileSaveInput.capture(revision: rev, name: profile.displayName, description: profile.description)
        do {
            let api = try resolveAPI()
            switch try await api.saveModelProfile(profile.id, input: input) {
            case .saved(let updated):
                catalog = updated
                unsupported = false
                error = nil
                actionError = nil
                conflict = false
                syncKey += 1
                await deps.updated()
                return true
            case .conflict:
                conflict = true
                actionError = String(localized: "Profiles were changed elsewhere. Reload to get the latest version before capturing.")
                return false
            }
        } catch {
            actionError = Self.errorMessage(error)
            return false
        }
    }

    /// Captures live settings into `active` (`PUT capture: true`), then applies `target` (`POST apply`) inside a single
    /// write-fenced section. Aborts apply if capture fails; retains captured state and reports the partial failure if apply fails.
    func captureActiveAndApply(active: ModelProfile, target: ModelProfile) async -> CaptureAndApplyResult {
        guard !busy else { return .captureFailed }
        busyAction = .captureAndApply
        actionError = nil
        conflict = false
        defer { busyAction = nil }
        let rev = catalog?.revision ?? 0
        let input = ModelProfileSaveInput.capture(revision: rev, name: active.displayName, description: active.description)
        let api: APIClient
        do {
            api = try resolveAPI()
        } catch {
            actionError = Self.errorMessage(error)
            return .captureFailed
        }
        do {
            switch try await api.saveModelProfile(active.id, input: input) {
            case .conflict:
                conflict = true
                actionError = String(localized: "Profiles were changed elsewhere. Reload to get the latest version before capturing.")
                return .captureFailed
            case .saved(let captured):
                catalog = captured
                unsupported = false
                error = nil
                actionError = nil
                conflict = false
                syncKey += 1
            }
        } catch {
            actionError = Self.errorMessage(error)
            return .captureFailed
        }
        do {
            let applied = try await api.applyModelProfile(target.id)
            catalog = applied
            unsupported = false
            error = nil
            actionError = nil
            conflict = false
            await deps.updated()
            return .applied
        } catch {
            let reason = Self.errorMessage(error)
            actionError = String(localized: "Saved current configuration into \(active.displayName), but applying failed: \(reason)")
            await deps.updated()
            return .applyFailedAfterCapture
        }
    }

    private static func errorMessage(_ error: Error) -> String {
        error.shown ?? error.localizedDescription
    }

    private func resolveAPI() throws -> APIClient {
        if let existing = requestAPI { return existing }
        guard let api = deps.api().map({ owner.client($0) }) else {
            throw APIError(status: 0, message: String(localized: "Not connected to the hub yet"))
        }
        requestAPI = api
        return api
    }
}
