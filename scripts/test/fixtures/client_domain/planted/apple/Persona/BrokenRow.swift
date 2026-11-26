@_spi(Boundary) import PersonaPlantedDomain
import PersonaDomainKit

/// App code breaking the rules outside the Domain targets. expected.txt lists the findings.
struct BrokenRow {
    func drop(_ item: Item, until: Instant, now: Now) {
        _ = item.snoozed(until: until, at: now)
    }
}

struct RowReader: ItemVisitor {
    mutating func visitItem(_ id: ItemID, urgency: Urgency, wakes: Instant) {}
}
