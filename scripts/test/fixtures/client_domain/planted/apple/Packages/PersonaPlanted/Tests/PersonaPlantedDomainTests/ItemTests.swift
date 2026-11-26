import PersonaDomainKit
import PersonaDomainKitAdapt
import PersonaPlantedDomain
import Testing

/// The test's own visitor: it keeps when the item wakes.
private struct Wakes: ItemVisitor {
    var wakes: Instant?

    mutating func visitItem(_ id: ItemID, urgency: Urgency, wakes: Instant) { self.wakes = wakes }
}

@Test func snoozeMovesWhenTheItemWakes() throws {
    let now = FixedClock(nanosecondsSinceEpoch: 100).now()
    guard case .ok(let id) = ItemID.parse("item-1") else { Issue.record("parse failed"); return }
    let item = Item.new(id: id, urgency: .routine, arrived: .parse(nanosecondsSinceEpoch: 50))
    guard case .ok(let snoozed) = item.snoozed(until: .parse(nanosecondsSinceEpoch: 200), at: now) else {
        Issue.record("snooze refused")
        return
    }
    var reader = Wakes()
    snoozed.accept(&reader)
    #expect(reader.wakes == .parse(nanosecondsSinceEpoch: 200))
    #expect(item.needsOwner(at: now))
    #expect(!snoozed.needsOwner(at: now))
}
