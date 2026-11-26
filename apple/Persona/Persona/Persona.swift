import Foundation

/// A look for Persona (docs/apple/persona.md), the one Avatar setting: its name, its face, the animated dot in the Persona
/// tab's top bar, its colors and the home screen icon (`appIcon`). Mochi is drawn locally in the app icon's colors; Milk Frog is a
/// package on the hub that the client downloads and keeps (`PersonaStore`).
nonisolated struct Persona: Identifiable, Sendable, Hashable {
    /// The default's id: Mochi, distinct from every optional built-in design.
    static let builtinID = PersonaSelection.builtinID
    /// The default: Mochi, the app icon's character, in the icon's colors.
    static let builtin = Persona(id: builtinID, name: "Persona", subtitle: String(localized: "The waving blob from the app icon"), rev: "", manifest: nil,
                                 design: "mochi", title: String(localized: "Mochi"))
    /// Only Mochi is drawn locally; Milk Frog is supplied by the hub.
    static let builtins = [builtin]

    var id: String
    var name: String
    var subtitle: String?
    /// The package's rev on the hub (any byte changes it); "" for a built-in.
    var rev: String
    /// Nil for a built-in.
    var manifest: PersonaManifest?
    /// A built-in's PersonaAvatar.Design (raw value); nil: the default's.
    var design: String?
    /// The card's name in Settings when it is not `name` (the built-ins are all called Persona).
    var title: String?

    var isBuiltin: Bool { manifest == nil }
    /// Made by the hub from an image the user uploaded (docs/apple/persona.md "Uploading an image"): the only kind the
    /// apps can delete.
    var isUploaded: Bool { manifest?.isUploaded == true }
    var cardTitle: String { title ?? name }
    /// A built-in's line under its card in Settings: whose colors it is drawn in.
    var builtinNote: String { String(localized: "The app icon's colors") }
    var palette: PersonaPalette? {
        manifest?.colors.flatMap { PersonaPalette(light: $0.light, dark: $0.dark) }
    }
    /// The home screen icon (docs/apple/persona.md「App 图标」): a built-in's own; a package's `app_icon_fallback`, the
    /// built-in whose icon it takes, as iOS switches only to icons the app ships; else the default's.
    var appIcon: AppIconChoice {
        let look = isBuiltin ? id : manifest?.appIconFallback
        return AppIconChoice(lookID: look.map(NativeStateMigration.lookID))
    }

    init(id: String, name: String, subtitle: String?, rev: String, manifest: PersonaManifest?, design: String? = nil,
         title: String? = nil) {
        self.id = id
        self.name = name
        self.subtitle = subtitle
        self.rev = rev
        self.manifest = manifest
        self.design = design
        self.title = title
    }

    init(info: PersonaInfo) {
        self.init(id: info.id, name: info.manifest.name, subtitle: info.manifest.subtitle, rev: info.rev, manifest: info.manifest)
    }
}

/// persona.json, format 1 (docs/apple/persona.md「Pack format」).
nonisolated struct PersonaManifest: Codable, Sendable, Hashable {
    static let format = 1

    var format: Int
    var id: String
    var name: String
    var subtitle: String?
    /// A few words on the look's card in Settings › Appearance › Avatar, in place of its colors (Milk Frog: where its level comes from).
    var about: String?
    var avatar: Avatar
    var dot: Dot
    var colors: Colors?
    /// The built-in Avatar (its id) whose home screen icon this one puts on: the package's own pictures cannot be one.
    var appIconFallback: String?
    /// Where the package came from, when its maker says (`{"kind":"upload"}`: made by the hub from an uploaded image).
    var source: Source?

    enum CodingKeys: String, CodingKey {
        case format, id, name, subtitle, about, avatar, dot, colors, source
        case appIconFallback = "app_icon_fallback"
    }

    /// Made by the hub from an uploaded image (`POST /api/v1/looks/uploads`).
    var isUploaded: Bool { source?.kind == Source.upload }

    /// `source`: read leniently, so a package whose `source` is something else is still listed. An uploaded avatar's
    /// also says what its owner chose (#15449), for its Edit sheet: the theme color's seed and whether it is
    /// automatic, and which states have a picture of their own, each with its mark on or off.
    struct Source: Codable, Sendable, Hashable {
        static let upload = "upload"
        var kind: String?
        /// The theme color's seed and whether the hub took it from the picture; nil when the package says nothing of it.
        var color: Color?
        /// The states with a picture of their own (raw values), each with its mark; nil for one made before.
        var states: [String: State]?

        struct Color: Codable, Sendable, Hashable {
            /// "#RRGGBB".
            var seed: String?
            var auto: Bool
        }

        /// `{"own": true, "mark"}`: the state has a picture of its own.
        struct State: Codable, Sendable, Hashable {
            var own: Bool
            /// The hub draws the state's mark on the picture (a thought bubble, the working arc…).
            var mark: Bool

            init(own: Bool = true, mark: Bool) {
                self.own = own
                self.mark = mark
            }

            init(from decoder: any Decoder) throws {
                let c = try? decoder.container(keyedBy: CodingKeys.self)
                own = c.flatMap { try? $0.decodeIfPresent(Bool.self, forKey: .own) } ?? true
                mark = c.flatMap { try? $0.decodeIfPresent(Bool.self, forKey: .mark) } ?? true
            }
        }

        enum CodingKeys: String, CodingKey { case kind, color, states }

        init(kind: String?, color: Color? = nil, states: [String: State]? = nil) {
            self.kind = kind
            self.color = color
            self.states = states
        }

        init(from decoder: any Decoder) throws {
            let c = try? decoder.container(keyedBy: CodingKeys.self)
            kind = c.flatMap { try? $0.decodeIfPresent(String.self, forKey: .kind) }
            color = c.flatMap { try? $0.decodeIfPresent(Color.self, forKey: .color) }
            states = c.flatMap { try? $0.decodeIfPresent([String: State].self, forKey: .states) }
        }
    }

    struct Avatar: Codable, Sendable, Hashable {
        /// Side in pixels ("128") → file.
        var sizes: [String: String]
        /// The share of the circle what matters stays inside (1: the whole inscribed circle).
        var safe: Double?
    }

    struct Dot: Codable, Sendable, Hashable {
        /// The frames' side in pixels.
        var size: Int?
        /// State (raw value) → its frames.
        var states: [String: DotState]
    }

    struct DotState: Codable, Sendable, Hashable {
        /// An APNG (or a still PNG: one frame).
        var file: String
        var fps: Double?
    }

    /// "#RRGGBB" per token, per appearance.
    struct Colors: Codable, Sendable, Hashable {
        var light: [String: String]?
        var dark: [String: String]?
    }

    /// Every file it names (what a phone downloads).
    var files: [String] {
        Array(Set(avatar.sizes.values).union(dot.states.values.map(\.file))).sorted()
    }

    /// The avatar file for a face `points` wide at `scale`: the smallest at least that many pixels, else the largest.
    func avatarFile(points: Double, scale: Double) -> String? {
        let want = points * scale
        let sizes = avatar.sizes.compactMap { k, v in Double(k).map { ($0, v) } }.sorted { $0.0 < $1.0 }
        return (sizes.first { $0.0 >= want } ?? sizes.last)?.1
    }

    /// The state's frames, idle's for a state the package lacks.
    func dotState(_ s: PersonaState) -> DotState? { dot.states[s.rawValue] ?? dot.states[PersonaState.idle.rawValue] }

    /// What the hub checks too (server/internal/hub/persona.go), so a package it would leave out is refused here.
    var problem: String? {
        if format != Self.format { return "format \(format)" }
        if id == Persona.builtinID || PersonaSelection.resolve(id) != id || id.range(of: "^[a-z0-9][a-z0-9-]{0,31}$", options: .regularExpression) == nil { return "id \(id)" }
        if name.trimmingCharacters(in: .whitespaces).isEmpty { return "no name" }
        if avatar.sizes.isEmpty { return "no avatar" }
        if dot.states[PersonaState.idle.rawValue] == nil { return "no idle dot" }
        for f in files where f.isEmpty || f.hasPrefix("/") || f.split(separator: "/").contains(where: { $0 == ".." || $0.hasPrefix(".") }) {
            return "file \(f)"
        }
        return nil
    }
}

/// One entry of GET /api/v1/personas.
nonisolated struct PersonaInfo: Codable, Sendable, Hashable {
    var id: String
    var rev: String
    var bytes: Int64
    var manifest: PersonaManifest
}
