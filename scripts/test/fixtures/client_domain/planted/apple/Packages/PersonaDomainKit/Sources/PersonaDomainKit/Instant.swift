/// A point in time, in UTC nanoseconds.
public struct Instant: Comparable, Hashable, Sendable {
    private let instant: Int64
    private init(instant: Int64) { self.instant = instant }
    public static func parse(nanosecondsSinceEpoch: Int64) -> Instant { Instant(instant: nanosecondsSinceEpoch) }
    public static func < (earlier: Instant, later: Instant) -> Bool { earlier.instant < later.instant }
    @_spi(Boundary) public var wire: Int64 { instant }
}

/// The application's reading of the clock. Only PersonaDomainKitAdapt makes one, so a core never reads the time.
public struct Now: Sendable {
    private let now: Instant
    @_spi(Clock) public init(reading: Instant) { self.now = reading }
    public func reached(_ deadline: Instant) -> Bool { !(now < deadline) }
}
