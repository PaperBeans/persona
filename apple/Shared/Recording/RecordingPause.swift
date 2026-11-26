import Foundation

/// Why a stored recording's in-flight state (an upload not complete, minutes or a day recap pending) is not moving: the
/// hub's `paused` field on a recording's knowledge list entry and detail (#15214; docs/assistant/knowledge.md
/// "Recordings without the recording module"). Derived by the hub at read time, absent when nothing is paused.
///
/// A closed set with an explicit unknown: a reason a newer hub names is kept as it came and treated as "not paused for a
/// reason this app knows", so it neither fails the list nor shows the module-off words.
nonisolated enum RecordingPause: Hashable, Sendable, Codable {
    /// The hub's recording module is off: nothing writes the minutes or takes the rest of the upload until it is back.
    case moduleOff
    /// A reason this app doesn't know, as the hub wrote it.
    case unknown(String)

    /// The hub's word for `moduleOff` (`kb.PausedModuleOff`).
    static let moduleOffWire = "module_off"

    /// From the hub's word; nil for none (absent or empty).
    init?(wire: String?) {
        guard let wire, !wire.isEmpty else { return nil }
        self = wire == Self.moduleOffWire ? .moduleOff : .unknown(wire)
    }

    /// Read leniently: a value that is no string is an unknown reason, never a failed list or detail.
    init(from decoder: Decoder) throws {
        let wire = (try? decoder.singleValueContainer().decode(String.self)) ?? ""
        self = wire == Self.moduleOffWire ? .moduleOff : .unknown(wire)
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.singleValueContainer()
        switch self {
        case .moduleOff: try c.encode(Self.moduleOffWire)
        case .unknown(let wire): try c.encode(wire)
        }
    }

    /// The recording module being off is what holds it.
    var isModuleOff: Bool { self == .moduleOff }
}
