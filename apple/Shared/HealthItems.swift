import Foundation
import PersonaWire

/// The manager owns selection and revisions; clients keep IDs they cannot draw when editing.
nonisolated struct HealthItems: Codable, Sendable, Equatable {
    static let feature = "health_items"
    struct CatalogItem: Codable, Sendable, Equatable {
        var id: String
        var source: String
    }
    var catalog: [CatalogItem]
    var items: [String]
    var revision: Int
    var saved: Bool

    /// Replace only this device's slots. Hidden IDs keep their order, including unknown future items.
    static func merging(_ selected: [String], into original: [String], editable: Set<String>) -> [String] {
        var remaining = selected.makeIterator()
        var result: [String] = []
        for id in original {
            if editable.contains(id) {
                if let next = remaining.next() { result.append(next) }
            } else { result.append(id) }
        }
        while let next = remaining.next() { result.append(next) }
        return result
    }
}

extension APIClient {
    func healthItems() async throws -> HealthItems {
        try await manager(nil).get("/api/v1/assistant/health/items", query: ["space": Session.healthSpace])
    }

    func saveHealthItems(_ items: [String], revision: Int) async throws -> HealthItems {
        struct Save: Encodable { var items: [String]; var revision: Int }
        let data = try await manager(nil).request("PUT", "/api/v1/assistant/health/items",
                                                query: ["space": Session.healthSpace], body: Save(items: items, revision: revision))
        return try JSON.decoder.decode(HealthItems.self, from: data)
    }
}
