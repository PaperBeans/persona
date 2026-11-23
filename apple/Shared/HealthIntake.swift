import Foundation
import PersonaWire

/// The default manager draws these pages with the extension renderer, under an id no extension can own.
nonisolated enum HealthIntakeUI {
    static let id = "-health-intake"
    static let page = "pages"
    static let path = "/api/v1/assistant/health/intake/ui"

    static func state(_ kind: HealthIntake.Kind) -> String {
        kind == .water ? #"{"kind":"water"}"# : #"{"kind":"protein"}"#
    }
}

/// Persona's own intake only; the hub decides which date bucket is today.
nonisolated struct HealthIntake: Decodable, Sendable, Equatable {
    static let feature = "health_intake"
    nonisolated enum Kind: String, Codable, Sendable { case water, protein }
    nonisolated struct Entry: Codable, Sendable, Equatable {
        var id: String
        var kind: Kind
        var amount: Double
        var at: String
        var note: String
        var seq: Int64
        var createdAt: String
        var deletedAt: String?
        private nonisolated enum CodingKeys: String, CodingKey {
            case id, kind, amount, at, note, seq
            case createdAt = "created_at", deletedAt = "deleted_at"
        }
    }
    nonisolated struct Portion: Decodable, Sendable, Equatable {
        var today: Double
        var goal: Int
        var entries: [Entry]
    }
    nonisolated struct Day: Decodable, Sendable, Equatable {
        var date: String
        var water: Double
        var protein: Double
    }
    nonisolated struct Totals: Decodable, Sendable, Equatable {
        var water: Double
        var protein: Double
    }
    nonisolated struct Change: Decodable, Sendable {
        var entry: Entry?
        var today: Totals
        var zone: String
    }
    /// Made once per user action. Repeating this exact request keeps the UUID and timestamp.
    nonisolated struct Draft: Encodable, Sendable {
        var id = UUID().uuidString.lowercased()
        var kind: Kind
        var amount: Double?
        var at = Date.now.formatted(.iso8601)
    }
    var water: Portion
    var protein: Portion
    var days: [Day]
    var zone: String

    mutating func align(_ change: Change) {
        water.today = change.today.water
        protein.today = change.today.protein
        zone = change.zone
    }
}

extension APIClient {
    func healthIntake() async throws -> HealthIntake {
        try await manager(nil).get("/api/v1/assistant/health/intake", query: ["space": Session.healthSpace])
    }

    func recordHealthIntake(_ draft: HealthIntake.Draft) async throws -> HealthIntake.Change {
        let data = try await manager(nil).request("POST", "/api/v1/assistant/health/intake",
                                                query: ["space": Session.healthSpace], body: draft)
        return try JSON.decoder.decode(HealthIntake.Change.self, from: data)
    }

    func undoHealthIntake(_ id: String) async throws -> HealthIntake.Change {
        let data = try await manager(nil).request("DELETE", "/api/v1/assistant/health/intake/\(id)",
                                                query: ["space": Session.healthSpace])
        return try JSON.decoder.decode(HealthIntake.Change.self, from: data)
    }
}
