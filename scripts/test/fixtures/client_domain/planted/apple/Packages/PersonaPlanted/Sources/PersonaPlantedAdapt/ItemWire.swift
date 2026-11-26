import Foundation
@_spi(Boundary) import PersonaDomainKit
@_spi(Boundary) import PersonaPlantedDomain

/// An item as the hub sends and stores it.
public struct ItemWire: Codable, Equatable, Sendable {
    public var id: String
    public var urgent: Bool
    public var wakesAt: Int64

    public init(id: String, urgent: Bool, wakesAt: Int64) {
        self.id = id
        self.urgent = urgent
        self.wakesAt = wakesAt
    }
}

/// Converts between ItemWire and the planted domain: parse on the way in, visit on the way out.
public enum ItemAdapt {
    public static func item(from wire: ItemWire) -> Outcome<Item, PlantedFail> {
        switch ItemID.parse(wire.id) {
        case .ok(let id):
            return .ok(.new(id: id, urgency: wire.urgent ? .urgent : .routine,
                            arrived: .parse(nanosecondsSinceEpoch: wire.wakesAt)))
        case .failed(let fail):
            return .failed(fail)
        }
    }

    public static func wire(_ item: Item) -> ItemWire {
        var reader = WireReader()
        item.accept(&reader)
        return reader.wire
    }
}

private struct WireReader: ItemVisitor {
    var wire = ItemWire(id: "", urgent: false, wakesAt: 0)

    mutating func visitItem(_ id: ItemID, urgency: Urgency, wakes: Instant) {
        wire = ItemWire(id: id.wire, urgent: urgency.breaksQuiet, wakesAt: wakes.wire)
    }
}
