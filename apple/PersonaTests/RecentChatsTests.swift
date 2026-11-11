import Foundation
import Testing
@testable import Persona

/// Recently opened conversations (#2191): every case of spec/fixtures/recents/recent_conversations.json (the rules the
/// web's ⌘K runs too), then the device's list as the home screen menu reads it.
@MainActor struct RecentChatsTests {
    private static let fixtureURL = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()
        .deletingLastPathComponent().appending(path: "spec/fixtures/recents/recent_conversations.json")

    private struct Fixture: Decodable {
        let maxEntries: Int
        let homeScreen: Int
        let cases: [Case]
        enum CodingKeys: String, CodingKey {
            case cases
            case maxEntries = "max_entries"
            case homeScreen = "home_screen"
        }
    }

    private struct Case: Decodable {
        let name: String
        let operation: String
        let input: Input
        let expected: Expected
    }

    private struct Input: Decodable {
        var before: [String]?
        var opened: String?
        var entries: [String]?
        var current: String?
        var existing: [String]?
        var limit: Int?
    }

    private struct Expected: Decodable {
        var after: [String]?
        var shown: [String]?
    }

    private static func fixture() throws -> Fixture {
        try JSONDecoder().decode(Fixture.self, from: Data(contentsOf: fixtureURL))
    }

    @Test func limitsAndOperations() throws {
        let f = try Self.fixture()
        #expect(RecentChats.maxEntries == f.maxEntries)
        #expect(RecentChats.homeScreen == f.homeScreen)
        #expect(Set(f.cases.map(\.operation)) == ["open", "shown"])
    }

    @Test func everyCase() throws {
        for c in try Self.fixture().cases {
            switch c.operation {
            case "open":
                #expect(RecentChats.open(c.input.before ?? [], c.input.opened ?? "") == c.expected.after, "\(c.name)")
            default:
                let existing = Set(c.input.existing ?? [])
                #expect(RecentChats.shown(c.input.entries ?? [], current: c.input.current ?? "", exists: { existing.contains($0) },
                                          limit: c.input.limit ?? 0) == c.expected.shown, "\(c.name)")
            }
        }
    }

    /// The device's list: a chat on screen goes first and is the current one; leaving it keeps it; the home screen gets
    /// the newest the app still has, the one on screen included, other hubs' with their group.
    @Test func theDevicesList() {
        let store = RecentChatsStore(entries: [])
        let a = SessionKey(machineID: "m1", sessionID: "a")
        let b = SessionKey(group: "work", machineID: "m2", sessionID: "b")
        let gone = SessionKey(machineID: "m1", sessionID: "gone")
        store.shown(a)
        store.shown(gone)
        store.shown(b)
        store.shown(nil)
        #expect(store.entries == [b.path, gone.path, a.path])
        #expect(store.current.isEmpty)
        #expect(store.latest(2) { $0 != gone } == [b, a])
        store.shown(a)
        #expect(store.entries == [a.path, b.path, gone.path])
        #expect(store.current == a.path)
    }
}
