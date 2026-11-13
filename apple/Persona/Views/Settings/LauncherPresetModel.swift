import Foundation
import Observation

/// Settings › Model Providers › Add › From Extension (docs/extensions/extensions.md「Model providers」): the providers the hub's plugins offer; pick one,
/// give the launcher a name and a key (and maybe another of the provider's models), and the hub fills in the rest
/// (POST /api/v1/launchers with `preset`).
@Observable final class LauncherPresetModel {
    struct Deps {
        var api: () -> APIClient?
        static var live: Deps {
            let app = AppModel.shared
            let scope = AssistantSettingsRequestScope(app)
            return .init(api: { scope.api(app) })
        }
    }

    private(set) var presets: [LauncherPreset] = []
    private(set) var loaded = false
    private(set) var saving = false
    private(set) var error: String?
    /// The picked provider; picking another resets the model to its default.
    var selectedID: String? {
        didSet { if selectedID != oldValue { model = selected?.defaultModel ?? "" } }
    }
    var name = ""
    var apiKey = ""
    /// One of the provider's models (its default when none was picked).
    var model = ""
    @ObservationIgnored private let deps: Deps

    init(deps: Deps = .live) { self.deps = deps }

    var selected: LauncherPreset? { presets.first { $0.id == selectedID } }
    private var trimmedName: String { name.trimmingCharacters(in: .whitespaces) }

    /// What is wrong with the name as typed, the way the hub would put it; nil while empty or fine.
    var nameProblem: String? { LauncherName.problem(name) }

    var canSave: Bool {
        selected != nil && !trimmedName.isEmpty && nameProblem == nil
            && !apiKey.trimmingCharacters(in: .whitespaces).isEmpty && !saving
    }

    /// What is sent: the name, the key, the preset, and the model only when another one was picked; the hub fills in
    /// the address, the kind and the rest from the preset.
    var input: LauncherInput? {
        guard let p = selected else { return nil }
        let m = model.trimmingCharacters(in: .whitespaces)
        return LauncherInput(name: trimmedName, baseURL: "", model: m == p.defaultModel ? "" : m, smallModel: "", subagentModel: "",
                             customModel: "", customModelName: "", customModelDescription: "", args: [],
                             apiKey: apiKey.trimmingCharacters(in: .whitespaces), preset: p.id)
    }

    func load() async {
        do {
            guard let api = deps.api() else { throw APIError(status: 0, message: String(localized: "Not connected to the hub yet")) }
            presets = try await api.launcherPresets()
            if selected == nil { selectedID = presets.first?.id }
            error = nil
        } catch {
            self.error = error.shown
        }
        loaded = true
    }

    /// Adds the launcher; false (with `error`) when the hub refused it.
    func save() async -> Bool {
        guard canSave, let input, let api = deps.api() else { return false }
        saving = true
        defer { saving = false }
        do {
            try await api.saveLauncher(input, isNew: true)
            error = nil
            return true
        } catch {
            self.error = error.shown ?? error.localizedDescription
            return false
        }
    }
}

/// How a preset reads in From Extension.
enum LauncherPresetText {
    /// "Direct API · api.example.com", "Claude Code Launcher · gateway.example.com".
    static func subtitle(_ p: LauncherPreset) -> String {
        let kind = p.kind == "api" ? String(localized: "Direct API") : String(localized: "Claude Code Launcher")
        guard let host = URL(string: p.baseURL)?.host(), !host.isEmpty else { return kind }
        return "\(kind) · \(host)"
    }
}
