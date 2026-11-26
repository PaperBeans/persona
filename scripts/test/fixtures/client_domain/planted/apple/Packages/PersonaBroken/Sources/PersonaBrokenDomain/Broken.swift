import Foundation
@_spi(Clock) import PersonaDomainKit
import PersonaBrokenSupport
import PersonaPlantedDomain

public enum State { case working }

public struct Broken: Codable, CustomStringConvertible {
    public let title: String
    public private(set) var count: Int
    public var doneAt: Date { Date(timeIntervalSince1970: 0) }
    public var description: String { title }

    public init(title: String, count: Int) {
        self.title = title
        self.count = count
    }

    public static func make(a: Instant, b: Instant, c: Instant, d: Instant, e: Instant) -> Broken {
        Broken(title: "", count: 0)
    }

    public mutating func bump() { count += 1 }

    public func save() throws { print(title) }

    public static func evolve(_ broken: Broken, _ state: State) -> Broken { broken }
}

public struct Box<Wrapped: Sendable>: Sendable {
    private let box: Wrapped
}

public extension Broken {
    enum Hidden { case hidden }
}

/// The rules on a value's API beyond its fields: what it hands out whatever the parameters, its id, its factories,
/// its callbacks and its boundary.
public struct Probe: Sendable {
    private let at: Instant
    private init(at: Instant) { self.at = at }

    public static func blank() -> Probe { Probe(at: .parse(nanosecondsSinceEpoch: 0)) }

    public var id: Instant { at }

    public func wakes(at now: Now) -> Instant { at }

    public subscript(_ item: ItemID) -> Instant { at }

    @discardableResult public func touched() -> Probe { self }

    public func whenDue(_ effect: (Probe) -> Void) { effect(self) }

    public func pick(_ options: [Probe]) -> Probe { options.shuffled().first ?? self }

    @_spi(Boundary) public var raw: Instant { at }
}

/// State that changes in place, and ports and visitors of the wrong shape.
public final class Tally: @unchecked Sendable {
    nonisolated(unsafe) private static var seen: [Instant] = []
    private init() {}
}

actor Ledger {}

private let defaultProbe = Probe.blank()

public protocol ProbeStore {
    func keep(_ probe: Probe)
}

public protocol ProbeVisitor {
    mutating func read(_ probe: Probe)
}

/// Primitives beside other fields, a getter one step out, and factories that return nil or another type.
public struct Entry: Sendable {
    private let title: String, count: Int
    fileprivate let wakes: Instant

    private init(title: String, count: Int, wakes: Instant) {
        self.title = title
        self.count = count
        self.wakes = wakes
    }

    public static func wakes(of entry: Entry) -> Instant { entry.wakes }

    public static func from(_ wakes: Instant) -> Entry? { nil }

    public static func parse(_ raw: Int64) -> Instant { .parse(nanosecondsSinceEpoch: raw) }

    public static func restore(_ wakes: Instant) -> Entry { Entry(title: "", count: 0, wakes: wakes) }
}

public func wakesAt(_ entry: Entry) -> Instant { entry.wakes }

/// A state beside another field: not a closed set, so it names no factory for a case.
public struct Phase: Sendable {
    private enum Case: Sendable { case open, done(at: Instant, by: String) }
    private let phase: Case
    private let since: Instant

    private init(phase: Case, since: Instant) {
        self.phase = phase
        self.since = since
    }

    public static func draft(_ since: Instant) -> Phase { Phase(phase: .open, since: since) }
}

/// A primitive under another name beside another field, and a static wire and an operator that hand a field out.
public struct Label: Sendable {
    private typealias Raw = String
    private let raw: Raw, wakes: Instant

    private init(raw: Raw, wakes: Instant) {
        self.raw = raw
        self.wakes = wakes
    }

    public static func wire(_ label: Label) -> Instant { label.wakes }

    public static prefix func ~ (label: Label) -> Instant { label.wakes }
}
