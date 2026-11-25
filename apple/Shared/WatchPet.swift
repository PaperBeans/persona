import Foundation

/// What the watch's Milk Frog is doing (docs/apple/persona.md "The Milk Frog on the watch").
nonisolated enum WatchPetState: String, CaseIterable, Sendable {
    case needsYou = "needs_you", working, done, idle, sleep, offline

    /// The clip the state plays; the same name as its face and its raw value.
    var clip: String { rawValue }
}

nonisolated struct WatchPetStatus: Equatable, Sendable {
    var state: WatchPetState
    /// How many agents the state is about (waiting, working or finished).
    var count = 0

    var title: String {
        switch state {
        case .needsYou: StateName.count(.needsYou, count)
        case .working: StateName.count(.working, count)
        case .done: StateName.count(.unread, count)
        case .idle: String(localized: "Nothing going on [Watch Pet]")
        case .sleep: String(localized: "Asleep [Watch Pet]")
        case .offline: String(localized: "Can't reach the hub")
        }
    }
}

/// The pure decision behind the frog. It never reads as calm without knowing: a hub out of reach is not "nothing going
/// on". What a reachable hub says still counts when another one is out of reach.
nonisolated enum WatchPet {
    /// Only Milk Frog supplies the pet assets; other avatar packages remain ordinary watch choices.
    static func supports(_ persona: String?) -> Bool { persona == PersonaSelection.packageID }

    /// The clip played when the frog is tapped; it starts and ends on the idle pose.
    static let tapClip = "tap"
    static let manifestPath = "/api/v1/personas/\(PersonaSelection.packageID)/pet/pet.json"

    /// - Parameters:
    ///   - unreachable: no hub answered, so everything shown is old.
    ///   - partlyUnreachable: some hub did not answer, so the rest of what it would say is unknown.
    static func status(sessions: [Session], interactions: [Interaction], unreachable: Bool, partlyUnreachable: Bool,
                       now: Date = .now, calendar: Calendar = .current) -> WatchPetStatus {
        if unreachable { return WatchPetStatus(state: .offline) }
        // A card its task's supervisor holds (`Interaction.supervisorHeld`) is the supervisor's, not the user's:
        // neither the card nor a session in Needs You whose pending cards here are all held makes the frog wait
        // (`AgentsNeedsYou.rows`, D11). That session is still working under its supervisor.
        var held: Set<SessionKey> = []
        var open: Set<SessionKey> = []
        let waitingKeys = Set(sessions.filter { $0.state == .needsYou }.map(\.key))
        for ix in interactions {
            // A folded child's or side's card belongs to its parent row when that parent is waiting (`AgentsNeedsYou.rows`).
            let home = SessionKey(group: ix.group, machineID: ix.machineID, sessionID: ix.requestHome)
            let key = home != ix.sessionKey && waitingKeys.contains(home) ? home : ix.sessionKey
            if ix.supervisorHeld {
                if !ix.isSessionless { held.insert(key) }
                continue
            }
            open.insert(key)
        }
        let heldOnly = held.subtracting(open)
        let waiting = open.union(sessions.filter { $0.state == .needsYou && !heldOnly.contains($0.key) }.map(\.key))
        if !waiting.isEmpty { return WatchPetStatus(state: .needsYou, count: waiting.count) }
        let working = sessions.filter { $0.state == .working || ($0.state == .needsYou && heldOnly.contains($0.key)) }.count
        if working > 0 { return WatchPetStatus(state: .working, count: working) }
        if partlyUnreachable { return WatchPetStatus(state: .offline) }
        let finished = sessions.filter { $0.state == .unread }.count
        if finished > 0 { return WatchPetStatus(state: .done, count: finished) }
        return WatchPetStatus(state: isNight(now, calendar: calendar) ? .sleep : .idle)
    }

    static func isNight(_ date: Date, calendar: Calendar = .current) -> Bool {
        let hour = calendar.component(.hour, from: date)
        return hour >= 23 || hour < 7
    }
}

/// `pet/pet.json` in the Milk Frog's package: the clips and faces a watch fetches, by hash.
nonisolated struct WatchPetManifest: Codable, Equatable, Sendable {
    struct Clip: Codable, Equatable, Sendable {
        var file: String
        var sha256: String
        var fps: Double
        var frames: Int
        /// The frame that shows the state alone: all the always-on display draws.
        var still: Int
    }

    struct Face: Codable, Equatable, Sendable {
        var file: String
        var sha256: String
    }

    var format: Int
    var clips: [String: Clip]
    var faces: [String: Face]

    static let currentFormat = 1

    /// A format this build reads, with something to show for every state; the phone never gets half a look, nor does
    /// the wrist.
    var isUsable: Bool {
        let hashes = clips.values.map(\.sha256) + faces.values.map(\.sha256)
        return format == Self.currentFormat
            && WatchPetState.allCases.allSatisfy { clips[$0.clip] != nil && faces[$0.clip] != nil }
            && clips[WatchPet.tapClip] != nil
            && clips.values.allSatisfy { $0.fps > 0 && $0.frames > 0 && (0..<$0.frames).contains($0.still) }
            && hashes.allSatisfy(Self.isHash)
    }

    /// Hashes name the files on the watch, so only a plain SHA-256 may become a path.
    private static func isHash(_ s: String) -> Bool {
        s.count == 64 && s.allSatisfy { $0.isHexDigit }
    }
}
