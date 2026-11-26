import Foundation
import XCTest
@testable import PersonaLink

@MainActor
final class LinkRankingTests: XCTestCase {
    func testDuplicateURLKeepsTheFirstOccurrence() {
        let manualFirst = LinkModel.ranked([
            LinkEndpoint(url: "wss://hub.example:7700", kind: "lan", manual: true),
            LinkEndpoint(url: "wss://hub.example:7700", kind: "lan", manual: nil),
        ])
        XCTAssertEqual(manualFirst.map(\.url), ["wss://hub.example:7700"])
        XCTAssertEqual(manualFirst.first?.manual, true)

        // First occurrence wins even when the hub-delivered copy comes first.
        let hubFirst = LinkModel.ranked([
            LinkEndpoint(url: "wss://hub.example:7700", kind: "lan", manual: nil),
            LinkEndpoint(url: "wss://hub.example:7700", kind: "lan", manual: true),
        ])
        XCTAssertEqual(hubFirst.map(\.url), ["wss://hub.example:7700"])
        XCTAssertNil(hubFirst.first?.manual)
    }

    func testKindsRankLanBeforeIPv6BeforeNameBeforeUnknown() {
        let ranked = LinkModel.ranked([
            LinkEndpoint(url: "wss://name.example:7700", kind: "name", manual: nil),
            LinkEndpoint(url: "wss://lan.example:7700", kind: "lan", manual: nil),
            LinkEndpoint(url: "wss://unknown.example:7700", kind: "ipv4", manual: nil),
            LinkEndpoint(url: "wss://ipv6.example:7700", kind: "ipv6", manual: nil),
        ])
        XCTAssertEqual(ranked.map(\.kind), ["lan", "ipv6", "name", "ipv4"])
    }

    func testManualComesFirstWithinAKind() {
        let ranked = LinkModel.ranked([
            LinkEndpoint(url: "wss://hub-a.example:7700", kind: "lan", manual: nil),
            LinkEndpoint(url: "wss://hub-b.example:7700", kind: "lan", manual: true),
        ])
        XCTAssertEqual(ranked.map(\.url), [
            "wss://hub-b.example:7700",
            "wss://hub-a.example:7700",
        ])
    }

    func testEqualKindAndManualKeepInputOrder() {
        let ranked = LinkModel.ranked([
            LinkEndpoint(url: "wss://hub-a.example:7700", kind: "lan", manual: nil),
            LinkEndpoint(url: "wss://hub-b.example:7700", kind: "lan", manual: nil),
            LinkEndpoint(url: "wss://hub-c.example:7700", kind: "lan", manual: nil),
        ])
        XCTAssertEqual(ranked.map(\.url), [
            "wss://hub-a.example:7700",
            "wss://hub-b.example:7700",
            "wss://hub-c.example:7700",
        ])
    }

    func testEmptyInputStaysEmpty() {
        XCTAssertTrue(LinkModel.ranked([]).isEmpty)
    }
}
