import Foundation
@_spi(Clock) import PersonaDomainKit

/// Reads the system clock once per call: the application's maker of Now.
public struct SystemClock: Sendable {
    public init() {}
    public func now() -> Now { Now(reading: .parse(nanosecondsSinceEpoch: Int64(Date().timeIntervalSince1970 * 1e9))) }
}

/// A clock stopped at one instant, for tests.
public struct FixedClock: Sendable {
    private let instant: Instant
    public init(nanosecondsSinceEpoch: Int64) { instant = .parse(nanosecondsSinceEpoch: nanosecondsSinceEpoch) }
    public func now() -> Now { Now(reading: instant) }
}
