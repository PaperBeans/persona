public import PersonaDomainKit

/// An item's identity. Raw input becomes one only through parse; the adapter alone reads it back (wire).
public struct ItemID: Hashable, Sendable {
    private let item: String
    private init(item: String) { self.item = item }

    public static func parse(_ raw: String) -> Outcome<ItemID, PlantedFail> {
        raw.allSatisfy(\.isWhitespace) ? .failed(.emptyID) : .ok(ItemID(item: raw))
    }

    @_spi(Boundary) public var wire: String { item }
}

/// How urgent an item is: a closed set, as a struct over a private enum, so no caller switches on it.
public struct Urgency: Hashable, Sendable {
    private enum Case: Hashable, Sendable { case routine, urgent }
    private let urgency: Case
    private init(_ urgency: Case) { self.urgency = urgency }

    public static let routine = Urgency(.routine)
    public static let urgent = Urgency(.urgent)

    /// Whether it may interrupt the owner's quiet hours.
    public var breaksQuiet: Bool { urgency == .urgent }
}

/// The planted context's expected failures.
public struct PlantedFail: Fail {
    private enum Case: Sendable { case emptyID, alreadyDue }
    private let fail: Case
    private init(_ fail: Case) { self.fail = fail }

    public static let emptyID = PlantedFail(.emptyID)
    public static let alreadyDue = PlantedFail(.alreadyDue)

    public func accept(_ v: inout some FailVisitor) {
        switch fail {
        case .emptyID: v.visitInvalid(.new("planted.item.empty_id"), .new("id"))
        case .alreadyDue: v.visitRefused(.new("planted.item.already_due"))
        }
    }
}

/// Reads an item for an adapter: what it is, how urgent, and when it comes back to the owner.
public protocol ItemVisitor {
    mutating func visitItem(_ id: ItemID, urgency: Urgency, wakes: Instant)
}

/// An item the owner may have to look at. It never throws, prints (print(...)) or reads the clock: a snooze takes Now
/// from its caller and returns the next item, or why it was refused.
public struct Item: Sendable {
    private let identity: ItemID
    private let urgency: Urgency
    private let wakes: Instant

    private init(identity: ItemID, urgency: Urgency, wakes: Instant) {
        self.identity = identity
        self.urgency = urgency
        self.wakes = wakes
    }

    public static func new(id: ItemID, urgency: Urgency, arrived: Instant) -> Item {
        Item(identity: id, urgency: urgency, wakes: arrived)
    }

    public var id: ItemID { identity }

    public func isSame(as other: Item) -> Bool { identity == other.identity }

    public func needsOwner(at now: Now) -> Bool { urgency.breaksQuiet || now.reached(wakes) }

    public func snoozed(until: Instant, at now: Now) -> Outcome<Item, PlantedFail> {
        now.reached(until) ? .failed(.alreadyDue) : .ok(Item(identity: identity, urgency: urgency, wakes: until))
    }

    public func accept(_ v: inout some ItemVisitor) {
        v.visitItem(identity, urgency: urgency, wakes: wakes)
    }
}

/// When an item comes back to the owner: at an instant, or never. A closed set whose case with data has a factory
/// named for the case.
public struct Wake: Sendable {
    private enum Case: Sendable { case at(Instant), never }
    private let wake: Case
    private init(_ wake: Case) { self.wake = wake }

    public static let never = Wake(.never)

    public static func at(_ instant: Instant) -> Wake { Wake(.at(instant)) }

    public func reached(by now: Now) -> Bool {
        switch wake {
        case .at(let instant): now.reached(instant)
        case .never: false
        }
    }
}

/// When a snoozed item comes back to the owner: not again if it already needs them, else when the snooze ends. A
/// domain service: it asks the item, and never reads its fields.
public func nextWake(of item: Item, snoozedUntil until: Instant, at now: Now) -> Wake {
    item.needsOwner(at: now) ? .never : .at(until)
}
