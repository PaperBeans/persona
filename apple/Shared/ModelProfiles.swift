import Foundation
import PersonaWire

/// Managed launcher model overrides inside a model profile (`launchers.<name>` in `GET/PUT /api/v1/model-profiles`).
/// Only non-nil fields are encoded; an empty string `""` explicitly clears that field on switch.
nonisolated struct ModelProfileLauncherModels: Codable, Hashable, Sendable {
    var model: String? = nil
    var smallModel: String? = nil
    var subagentModel: String? = nil
    var opusModel: String? = nil
    var sonnetModel: String? = nil
    var haikuModel: String? = nil
    var fableModel: String? = nil
    var effort: String? = nil

    enum CodingKeys: String, CodingKey {
        case model, effort
        case smallModel = "small_model"
        case subagentModel = "subagent_model"
        case opusModel = "opus_model"
        case sonnetModel = "sonnet_model"
        case haikuModel = "haiku_model"
        case fableModel = "fable_model"
    }

    var isEmpty: Bool {
        model == nil && smallModel == nil && subagentModel == nil && opusModel == nil
            && sonnetModel == nil && haikuModel == nil && fableModel == nil && effort == nil
    }

    var entries: [(field: String, value: String)] {
        var result: [(String, String)] = []
        if let model { result.append(("model", model)) }
        if let smallModel { result.append(("small_model", smallModel)) }
        if let subagentModel { result.append(("subagent_model", subagentModel)) }
        if let opusModel { result.append(("opus_model", opusModel)) }
        if let sonnetModel { result.append(("sonnet_model", sonnetModel)) }
        if let haikuModel { result.append(("haiku_model", haikuModel)) }
        if let fableModel { result.append(("fable_model", fableModel)) }
        if let effort { result.append(("effort", effort)) }
        return result
    }

    func value(for field: String) -> String? {
        switch field {
        case "model": model
        case "small_model": smallModel
        case "subagent_model": subagentModel
        case "opus_model": opusModel
        case "sonnet_model": sonnetModel
        case "haiku_model": haikuModel
        case "fable_model": fableModel
        case "effort": effort
        default: nil
        }
    }

    func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        if let model { try c.encode(model, forKey: .model) }
        if let smallModel { try c.encode(smallModel, forKey: .smallModel) }
        if let subagentModel { try c.encode(subagentModel, forKey: .subagentModel) }
        if let opusModel { try c.encode(opusModel, forKey: .opusModel) }
        if let sonnetModel { try c.encode(sonnetModel, forKey: .sonnetModel) }
        if let haikuModel { try c.encode(haikuModel, forKey: .haikuModel) }
        if let fableModel { try c.encode(fableModel, forKey: .fableModel) }
        if let effort { try c.encode(effort, forKey: .effort) }
    }
}

/// An Agent preset (`tiers.<name>`) inside a model profile.
/// Default (`"standard"`) speed and empty optional fields are omitted on encode so older hubs using strict JSON decoding accept saves cleanly.
nonisolated struct ModelProfileTier: Codable, Hashable, Sendable {
    var launcher: String
    var model: String? = nil
    var effort: String? = nil
    var speed: String? = nil
    var permissionMode: String? = nil
    var description: String? = nil

    enum CodingKeys: String, CodingKey {
        case launcher, model, effort, speed, description
        case permissionMode = "permission_mode"
    }

    init(launcher: String, model: String? = nil, effort: String? = nil, speed: String? = nil,
         permissionMode: String? = nil, description: String? = nil) {
        self.launcher = launcher
        self.model = model
        self.effort = effort
        self.speed = speed
        self.permissionMode = permissionMode
        self.description = description
    }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        launcher = try c.decodeIfPresent(String.self, forKey: .launcher) ?? ""
        model = try c.decodeIfPresent(String.self, forKey: .model)
        effort = try c.decodeIfPresent(String.self, forKey: .effort)
        speed = try c.decodeIfPresent(String.self, forKey: .speed)
        permissionMode = try c.decodeIfPresent(String.self, forKey: .permissionMode)
        description = try c.decodeIfPresent(String.self, forKey: .description)
    }

    func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(launcher, forKey: .launcher)
        if let model = Self.nonEmpty(model) { try c.encode(model, forKey: .model) }
        if let effort = Self.nonEmpty(effort) { try c.encode(effort, forKey: .effort) }
        if let speed = Self.nonEmpty(speed), speed != "standard" { try c.encode(speed, forKey: .speed) }
        if let permissionMode = Self.nonEmpty(permissionMode) { try c.encode(permissionMode, forKey: .permissionMode) }
        if let description = Self.nonEmpty(description) { try c.encode(description, forKey: .description) }
    }

    private static func nonEmpty(_ value: String?) -> String? {
        guard let trimmed = value?.trimmingCharacters(in: .whitespacesAndNewlines), !trimmed.isEmpty else { return nil }
        return trimmed
    }
}

nonisolated struct ModelProfileActor: Codable, Hashable, Sendable {
    var kind: String
    var name: String? = nil

    init(kind: String = "", name: String? = nil) {
        self.kind = kind
        self.name = name
    }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        kind = try c.decodeIfPresent(String.self, forKey: .kind) ?? ""
        name = try c.decodeIfPresent(String.self, forKey: .name)
    }
}

nonisolated struct ModelProfileEvent: Codable, Hashable, Sendable {
    var at: String? = nil
    var action: String
    var from: String? = nil
    var to: String? = nil
    var by: ModelProfileActor? = nil
    var changed: [String]? = nil

    init(at: String? = nil, action: String = "", from: String? = nil, to: String? = nil,
         by: ModelProfileActor? = nil, changed: [String]? = nil) {
        self.at = at
        self.action = action
        self.from = from
        self.to = to
        self.by = by
        self.changed = changed
    }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        at = try c.decodeIfPresent(String.self, forKey: .at)
        action = try c.decodeIfPresent(String.self, forKey: .action) ?? ""
        from = try c.decodeIfPresent(String.self, forKey: .from)
        to = try c.decodeIfPresent(String.self, forKey: .to)
        by = try c.decodeIfPresent(ModelProfileActor.self, forKey: .by)
        changed = try c.decodeIfPresent([String].self, forKey: .changed)
    }
}

nonisolated struct ModelProfile: Codable, Hashable, Identifiable, Sendable {
    var id: String
    var name: String
    var description: String? = nil
    var purposes: [String: ModelBinding] = [:]
    var launchers: [String: ModelProfileLauncherModels] = [:]
    var tiers: [String: ModelProfileTier] = [:]
    var updatedAt: String? = nil
    var active: Bool = false
    var changes: [String] = []

    var displayName: String {
        let trimmed = name.trimmingCharacters(in: .whitespacesAndNewlines)
        return trimmed.isEmpty ? id : trimmed
    }

    enum CodingKeys: String, CodingKey {
        case id, name, description, purposes, launchers, tiers, active, changes
        case updatedAt = "updated_at"
    }

    init(id: String, name: String = "", description: String? = nil, purposes: [String: ModelBinding] = [:],
         launchers: [String: ModelProfileLauncherModels] = [:], tiers: [String: ModelProfileTier] = [:],
         updatedAt: String? = nil, active: Bool = false, changes: [String] = []) {
        self.id = id
        self.name = name
        self.description = description
        self.purposes = purposes
        self.launchers = launchers
        self.tiers = tiers
        self.updatedAt = updatedAt
        self.active = active
        self.changes = changes
    }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? ""
        description = try c.decodeIfPresent(String.self, forKey: .description)
        purposes = try c.decodeIfPresent([String: ModelBinding].self, forKey: .purposes) ?? [:]
        launchers = try c.decodeIfPresent([String: ModelProfileLauncherModels].self, forKey: .launchers) ?? [:]
        tiers = try c.decodeIfPresent([String: ModelProfileTier].self, forKey: .tiers) ?? [:]
        updatedAt = try c.decodeIfPresent(String.self, forKey: .updatedAt)
        active = try c.decodeIfPresent(Bool.self, forKey: .active) ?? false
        changes = try c.decodeIfPresent([String].self, forKey: .changes) ?? []
    }
}

nonisolated struct ModelProfileUndo: Codable, Hashable, Sendable {
    var id: Int64? = nil
    var to: String? = nil
    var at: String? = nil
    var changes: [String] = []
    var redo: Bool? = nil

    init(id: Int64? = nil, to: String? = nil, at: String? = nil, changes: [String] = [], redo: Bool? = nil) {
        self.id = id
        self.to = to
        self.at = at
        self.changes = changes
        self.redo = redo
    }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decodeIfPresent(Int64.self, forKey: .id)
        to = try c.decodeIfPresent(String.self, forKey: .to)
        at = try c.decodeIfPresent(String.self, forKey: .at)
        changes = try c.decodeIfPresent([String].self, forKey: .changes) ?? []
        redo = try c.decodeIfPresent(Bool.self, forKey: .redo)
    }
}

nonisolated struct ModelProfilesCatalog: Codable, Hashable, Sendable {
    var revision: Int64 = 0
    var active: String? = nil
    var at: String? = nil
    var by: ModelProfileActor? = nil
    var rollback: ModelProfileUndo? = nil
    var rollbacks: [ModelProfileUndo]? = nil
    var profiles: [ModelProfile] = []
    var events: [ModelProfileEvent] = []

    init(revision: Int64 = 0, active: String? = nil, at: String? = nil, by: ModelProfileActor? = nil,
         rollback: ModelProfileUndo? = nil, rollbacks: [ModelProfileUndo]? = nil,
         profiles: [ModelProfile] = [], events: [ModelProfileEvent] = []) {
        self.revision = revision
        self.active = active
        self.at = at
        self.by = by
        self.rollback = rollback
        self.rollbacks = rollbacks
        self.profiles = profiles
        self.events = events
    }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        revision = try c.decodeIfPresent(Int64.self, forKey: .revision) ?? 0
        active = try c.decodeIfPresent(String.self, forKey: .active)
        at = try c.decodeIfPresent(String.self, forKey: .at)
        by = try c.decodeIfPresent(ModelProfileActor.self, forKey: .by)
        rollback = try c.decodeIfPresent(ModelProfileUndo.self, forKey: .rollback)
        rollbacks = try c.decodeIfPresent([ModelProfileUndo].self, forKey: .rollbacks)
        profiles = try c.decodeIfPresent([ModelProfile].self, forKey: .profiles) ?? []
        events = try c.decodeIfPresent([ModelProfileEvent].self, forKey: .events) ?? []
    }
}

/// Payload for `PUT /api/v1/model-profiles/{id}`.
/// Matches the server's `modelProfileInput` cleanly across hub versions: omits blank optional fields,
/// omits default `"standard"` tier speed, and never emits `runtime`, `config_dir`, or credentials.
nonisolated struct ModelProfileSaveInput: Encodable, Hashable, Sendable {
    var revision: Int64
    var name: String
    var description: String = ""
    var purposes: [String: ModelBinding]? = nil
    var launchers: [String: ModelProfileLauncherModels]? = nil
    var tiers: [String: ModelProfileTier]? = nil
    var capture: Bool = false

    /// Minimal `PUT /api/v1/model-profiles/{id}` body with `capture: true`: replaces purpose bindings and
    /// managed launcher overrides with the hub's live configuration while preserving `name`, optional
    /// `description`, and existing Agent presets (`tiers`).
    static func capture(revision: Int64, name: String, description: String? = nil) -> ModelProfileSaveInput {
        ModelProfileSaveInput(
            revision: revision,
            name: name.trimmingCharacters(in: .whitespacesAndNewlines),
            description: description?.trimmingCharacters(in: .whitespacesAndNewlines) ?? "",
            capture: true
        )
    }

    private struct EncodedPurposeBinding: Encodable {
        var provider: String?
        var model: String?
        var effort: String?
        var permissionMode: String?

        enum CodingKeys: String, CodingKey {
            case provider, model, effort
            case permissionMode = "permission_mode"
        }

        init(_ binding: ModelBinding) {
            provider = Self.nonEmpty(binding.provider)
            model = Self.nonEmpty(binding.model)
            effort = Self.nonEmpty(binding.effort)
            permissionMode = Self.nonEmpty(binding.permissionMode)
        }

        func encode(to encoder: any Encoder) throws {
            var c = encoder.container(keyedBy: CodingKeys.self)
            if let provider { try c.encode(provider, forKey: .provider) }
            if let model { try c.encode(model, forKey: .model) }
            if let effort { try c.encode(effort, forKey: .effort) }
            if let permissionMode { try c.encode(permissionMode, forKey: .permissionMode) }
        }

        private static func nonEmpty(_ value: String?) -> String? {
            guard let trimmed = value?.trimmingCharacters(in: .whitespacesAndNewlines), !trimmed.isEmpty else { return nil }
            return trimmed
        }
    }

    enum CodingKeys: String, CodingKey {
        case revision, name, description, purposes, launchers, tiers, capture
    }

    func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(revision, forKey: .revision)
        try c.encode(name, forKey: .name)
        let trimmedDescription = description.trimmingCharacters(in: .whitespacesAndNewlines)
        if !trimmedDescription.isEmpty {
            try c.encode(trimmedDescription, forKey: .description)
        }
        if let purposes, !purposes.isEmpty {
            try c.encode(purposes.mapValues(EncodedPurposeBinding.init), forKey: .purposes)
        }
        if let launchers {
            let nonEmpty = launchers.filter { !$0.value.isEmpty }
            if !nonEmpty.isEmpty { try c.encode(nonEmpty, forKey: .launchers) }
        }
        if let tiers, !tiers.isEmpty {
            try c.encode(tiers, forKey: .tiers)
        }
        if capture {
            try c.encode(true, forKey: .capture)
        }
    }
}

nonisolated enum ModelProfileSaveResult: Sendable {
    case saved(ModelProfilesCatalog)
    /// The revision moved on elsewhere; nothing was written.
    case conflict
}

extension APIClient {
    /// Reads the hub's model profiles catalog; returns `nil` on HTTP 404 (older hubs without model profiles).
    func modelProfiles() async throws -> ModelProfilesCatalog? {
        do {
            return try await get("/api/v1/model-profiles", as: ModelProfilesCatalog.self)
        } catch let error as APIError where error.status == 404 {
            return nil
        }
    }

    func saveModelProfile(_ id: String, input: ModelProfileSaveInput) async throws -> ModelProfileSaveResult {
        do {
            let data = try await request("PUT", Self.modelProfilePath(id), body: input)
            return .saved(try JSON.decoder.decode(ModelProfilesCatalog.self, from: data))
        } catch let error as APIError where error.status == 409 {
            return .conflict
        }
    }

    func applyModelProfile(_ id: String) async throws -> ModelProfilesCatalog {
        try await post(Self.modelProfilePath(id) + "/apply", as: ModelProfilesCatalog.self)
    }

    func rollbackModelProfile() async throws -> ModelProfilesCatalog {
        try await post("/api/v1/model-profiles/rollback", as: ModelProfilesCatalog.self)
    }

    private static func modelProfilePath(_ id: String) -> String {
        let encoded = id.addingPercentEncoding(withAllowedCharacters: .alphanumerics.union(CharacterSet(charactersIn: "-_."))) ?? id
        return "/api/v1/model-profiles/\(encoded)"
    }
}
