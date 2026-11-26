import PersonaDomainKit
import PersonaDomainKitAdapt
import Testing

@Test func aReadingReachesTheInstantItWasTakenAt() {
    let now = FixedClock(nanosecondsSinceEpoch: 100).now()
    #expect(now.reached(.parse(nanosecondsSinceEpoch: 100)))
    #expect(!now.reached(.parse(nanosecondsSinceEpoch: 101)))
}
