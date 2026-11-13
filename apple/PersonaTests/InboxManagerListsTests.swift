import Foundation
import Testing
@testable import Persona

/// The Inbox's Rules and Muted lists with several managers (docs/assistant/managers.md "Pages"): the current assistant's
/// own list alone, read with it named (the default too) and stamped with it; an offline one says so in the list's place;
/// another assistant becoming current starts the list over, and a read for the one before doesn't land. Remove and Unmute
/// go to the manager the entry was read from (still after another became the default), and one that fails to go comes
/// back. A reload read before a removal landed doesn't bring it back, and only the newest load lands.
@MainActor
struct InboxManagerListsTests {
    private static let home = HubManager(id: "assistant", name: "助手", online: true, isDefault: true)
    private static let work = HubManager(id: "m-2", name: "工作", online: true)
    private static let away = HubManager(id: "m-3", name: "家里", online: false)

    /// A hub whose managers each answer `path` with their own `key` list, each read naming its manager (the default too);
    /// m-3 is offline.
    private static func hub(_ path: String, _ key: String, mine: String, work: String) -> FakeHub {
        let hub = FakeHub()
        hub.on("GET", path) { req in
            switch req.headers["X-Persona-Manager"] {
            case "assistant": return Data(#"{"\#(key)":[\#(mine)]}"#.utf8)
            case "m-2": return Data(#"{"\#(key)":[\#(work)]}"#.utf8)
            default: throw APIError(status: 503, message: "assistant offline")
            }
        }
        return hub
    }

    /// The hub's client, with the first request `held` picks kept back until `gate` opens.
    private static func holdingClient(_ hub: FakeHub, _ gate: Gate, _ held: @escaping @Sendable (LocalRequest) -> Bool) -> APIClient {
        var client = hub.client
        let answer = client.transport!
        client.transport = { req in
            if held(req) { await gate.pass() }
            return try await answer(req)
        }
        return client
    }

    /// Keeps the first request through it back until `open()`; later ones pass.
    private actor Gate {
        private var passed = 0
        private var held: CheckedContinuation<Void, Never>?
        private var watching: [CheckedContinuation<Void, Never>] = []

        func pass() async {
            passed += 1
            guard passed == 1 else { return }
            await withCheckedContinuation { c in
                held = c
                watching.forEach { $0.resume() }
                watching = []
            }
        }

        /// Returns once the first request is held.
        func holding() async {
            if held != nil { return }
            await withCheckedContinuation { watching.append($0) }
        }

        func open() {
            held?.resume()
            held = nil
        }
    }

    private static let myRule = #"{"id":"p1","source":"mail","tag":"sender","value":"a@example.com","name":"甲","tier":"action","created_at":"2026-11-04T01:00:00Z"}"#
    private static let workRule = #"{"id":"p1","source":"mail","tag":"sender","value":"b@example.com","name":"乙","tier":"noise","created_at":"2026-11-05T01:00:00Z"}"#
    private static let myMute = #"{"id":"m1","source":"github","tag":"repo","value":"example-org/one","name":"example-org/one","created_at":"2026-11-04T01:00:00Z"}"#
    private static let workMute = #"{"id":"m1","source":"github","tag":"repo","value":"example-org/two","name":"example-org/two","created_at":"2026-11-05T01:00:00Z"}"#

    // MARK: Rules

    @Test func theCurrentAssistantsRulesAloneAreReadWithItNamedAndRemovedOnIt() async throws {
        let hub = Self.hub("/api/v1/inbox/policies", "policies", mine: Self.myRule, work: Self.workRule)
        var named: [String?] = []
        hub.on("DELETE", "/api/v1/inbox/policies/p1") { req in
            named.append(req.headers["X-Persona-Manager"])
            return Data(#"{"ok":true}"#.utf8)
        }
        var toasts: [Toast] = []
        let m = InboxRulesModel(deps: .init(api: { hub.client }, toast: { toasts.append($0) }, current: { Self.work }))
        await m.load()
        #expect(hub.sent("GET", "/api/v1/inbox/policies").map { $0.headers["X-Persona-Manager"] } == ["m-2"], "one read, of the current one")
        #expect(m.state.value?.map(\.name) == ["乙"], "not the default's rule")
        #expect(m.state.value?.map(\.manager) == ["m-2"], "stamped with the manager it was read from")

        await m.remove(try #require(m.state.value?.first))
        #expect(named == ["m-2"])
        #expect(m.isEmpty && toasts.map(\.message) == ["已移除“乙”的规则"])
    }

    @Test func theDefaultCurrentIsNamedTooAndItsEntriesGoToItAfterAnotherBecameTheDefault() async throws {
        // The default may have moved on another device: a read naming none would get that one's rules and mutes.
        let hub = Self.hub("/api/v1/inbox/policies", "policies", mine: Self.myRule, work: Self.workRule)
        hub.on("GET", "/api/v1/inbox/mutes") { req in
            req.headers["X-Persona-Manager"] == "assistant" ? Data(#"{"mutes":[\#(Self.myMute)]}"#.utf8) : Data(#"{"mutes":[\#(Self.workMute)]}"#.utf8)
        }
        var named: [String?] = []
        for path in ["/api/v1/inbox/policies/p1", "/api/v1/inbox/mutes/m1"] {
            hub.on("DELETE", path) { req in
                named.append(req.headers["X-Persona-Manager"])
                return Data(#"{"ok":true}"#.utf8)
            }
        }
        let rules = InboxRulesModel(deps: .init(api: { hub.client }, toast: { _ in }, current: { Self.home }))
        let mutes = InboxMutesModel(deps: .init(api: { hub.client }, current: { Self.home }, show: { _ in }))
        await rules.load()
        await mutes.load()
        #expect(rules.state.value?.map(\.name) == ["甲"] && mutes.state.value?.map(\.name) == ["example-org/one"])
        await rules.remove(try #require(rules.state.value?.first))
        await mutes.unmute(try #require(mutes.state.value?.first))
        #expect(named == ["assistant", "assistant"], "the manager each was read from, named whatever the default is now")
    }

    @Test func anOfflineCurrentAssistantSaysSoInTheListsPlace() async {
        let hub = Self.hub("/api/v1/inbox/policies", "policies", mine: Self.myRule, work: Self.workRule)
        hub.on("GET", "/api/v1/inbox/mutes") { _ in throw APIError(status: 503, message: "assistant offline") }
        let rules = InboxRulesModel(deps: .init(api: { hub.client }, toast: { _ in }, current: { Self.away }))
        let mutes = InboxMutesModel(deps: .init(api: { hub.client }, current: { Self.away }, show: { _ in }))
        await rules.load()
        await mutes.load()
        guard case .failed(let rule) = rules.state, case .failed(let mute) = mutes.state else {
            Issue.record("an offline assistant's lists read as \(rules.state) and \(mutes.state)")
            return
        }
        #expect(rule == "离线中：它回来后这里会显示它的规则。" && mute == "离线中：它回来后这里会显示它的静音。")
        #expect(!rules.isEmpty && !mutes.isEmpty, "not the empty list's hint")
    }

    @Test func aRuleThatFailsToGoComesBack() async throws {
        let hub = Self.hub("/api/v1/inbox/policies", "policies", mine: "", work: Self.workRule)
        hub.on("DELETE", "/api/v1/inbox/policies/p1") { _ in throw APIError(status: 400, message: "坏了") }
        let m = InboxRulesModel(deps: .init(api: { hub.client }, toast: { _ in }, current: { Self.work }))
        await m.load()
        await m.remove(try #require(m.state.value?.first))
        #expect(m.state.value?.map(\.id) == ["p1"])
    }

    @Test func aRemovedRuleStaysGoneWhenAReloadReadBeforeItLands() async throws {
        let hub = Self.hub("/api/v1/inbox/policies", "policies", mine: "", work: Self.workRule)
        hub.on("DELETE", "/api/v1/inbox/policies/p1") { _ in Data(#"{"ok":true}"#.utf8) }
        let gate = Gate()
        let client = Self.holdingClient(hub, gate) { $0.method == "DELETE" }
        let m = InboxRulesModel(deps: .init(api: { client }, toast: { _ in }, current: { Self.work }))
        await m.load()
        let rule = try #require(m.state.value?.first)
        let removing = Task { await m.remove(rule) }
        await gate.holding()
        await m.load() // read while the removal was on its way: the hub still had it
        #expect(m.state.value?.map(\.id) == ["p1"])
        await gate.open()
        await removing.value
        #expect(m.state.value?.isEmpty == true)
    }

    @Test func anotherAssistantCurrentStartsTheListOverAndTheReadForTheOneBeforeDoesntLand() async throws {
        let hub = Self.hub("/api/v1/inbox/policies", "policies", mine: Self.myRule, work: Self.workRule)
        let gate = Gate()
        let client = Self.holdingClient(hub, gate) { $0.headers["X-Persona-Manager"] == "assistant" }
        var current = Self.work
        let m = InboxRulesModel(deps: .init(api: { client }, toast: { _ in }, current: { current }))
        await m.load()
        #expect(m.state.value?.map(\.name) == ["乙"])
        let before = m.readKey

        current = Self.home // switched in the sidebar
        #expect(m.readKey != before, "the list is read again")
        let first = Task { await m.load() } // the default's rules hang
        await gate.holding()
        #expect(m.state.value == nil, "nothing of the assistant before stays on the page meanwhile")

        current = Self.work // and back, before that read answered
        await m.load()
        #expect(m.state.value?.map(\.name) == ["乙"])
        await gate.open()
        await first.value
        #expect(m.state.value?.map(\.name) == ["乙"], "the older read, for the assistant left, doesn't land")
    }

    @Test func aRuleOfTheAssistantLeftThatFailsToGoDoesntComeBackIntoTheNewOnesList() async throws {
        let hub = Self.hub("/api/v1/inbox/policies", "policies", mine: Self.myRule, work: Self.workRule)
        hub.on("DELETE", "/api/v1/inbox/policies/p1") { _ in throw APIError(status: 400, message: "坏了") }
        let gate = Gate()
        let client = Self.holdingClient(hub, gate) { $0.method == "DELETE" }
        var current = Self.work
        let m = InboxRulesModel(deps: .init(api: { client }, toast: { _ in }, current: { current }))
        await m.load()
        let rule = try #require(m.state.value?.first)
        let removing = Task { await m.remove(rule) }
        await gate.holding()
        current = Self.home
        await m.load()
        await gate.open()
        await removing.value
        #expect(m.state.value?.map(\.name) == ["甲"], "the default's rules alone: the other one's failed removal is not put among them")
    }

    // MARK: One manager

    @Test func oneManagerReadsAndChangesOneListAsBefore() async throws {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/inbox/policies", json: #"{"policies":[{"id":"p1","source":"mail","tag":"sender","value":"a@example.com","name":"甲","tier":"action"}]}"#)
        hub.on("GET", "/api/v1/inbox/mutes", json: #"{"mutes":[{"id":"u1","source":"github","tag":"repo","value":"example-org/one","name":"example-org/one"}]}"#)
        hub.on("DELETE", "/api/v1/inbox/policies/p1", json: #"{"ok":true}"#)
        hub.on("DELETE", "/api/v1/inbox/mutes/u1", json: #"{"ok":true}"#)
        let rules = InboxRulesModel(deps: .init(api: { hub.client }, toast: { _ in }))
        let mutes = InboxMutesModel(deps: .init(api: { hub.client }, current: { nil }, show: { _ in }))
        #expect(rules.readKey.isEmpty && mutes.readKey.isEmpty)
        await rules.load()
        await mutes.load()
        #expect(rules.state.value?.first?.manager == nil && mutes.state.value?.first?.manager == nil, "no stamp with one manager")
        await rules.remove(try #require(rules.state.value?.first))
        await mutes.unmute(try #require(mutes.state.value?.first))
        let named = [("GET", "/api/v1/inbox/policies"), ("GET", "/api/v1/inbox/mutes"), ("DELETE", "/api/v1/inbox/policies/p1"),
                     ("DELETE", "/api/v1/inbox/mutes/u1")].flatMap { hub.sent($0.0, $0.1).map { $0.headers["X-Persona-Manager"] } }
        #expect(named == [nil, nil, nil, nil])
    }

    // MARK: Muted

    @Test func theCurrentAssistantsMutesAloneAreReadWithItNamedAndTakenBackOnIt() async throws {
        let hub = Self.hub("/api/v1/inbox/mutes", "mutes", mine: Self.myMute, work: Self.workMute)
        var named: [String?] = []
        var failing = true
        hub.on("DELETE", "/api/v1/inbox/mutes/m1") { req in
            named.append(req.headers["X-Persona-Manager"])
            if failing { throw APIError(status: 400, message: "坏了") }
            return Data(#"{"ok":true}"#.utf8)
        }
        var shown: [String] = []
        let m = InboxMutesModel(deps: .init(api: { hub.client }, current: { Self.work }, show: { shown.append("\($0)") }))
        await m.load()
        #expect(hub.sent("GET", "/api/v1/inbox/mutes").map { $0.headers["X-Persona-Manager"] } == ["m-2"])
        #expect(m.state.value?.map(\.name) == ["example-org/two"] && m.state.value?.map(\.manager) == ["m-2"])

        await m.unmute(try #require(m.state.value?.first))
        #expect(m.state.value?.map(\.name) == ["example-org/two"] && shown.count == 1, "it failed: back, and said")
        failing = false
        await m.unmute(try #require(m.state.value?.first))
        #expect(named == ["m-2", "m-2"] && m.isEmpty)
    }

    @Test func anUnmuteStaysGoneWhenAReloadReadBeforeItLands() async throws {
        let hub = Self.hub("/api/v1/inbox/mutes", "mutes", mine: "", work: Self.workMute)
        hub.on("DELETE", "/api/v1/inbox/mutes/m1") { _ in Data(#"{"ok":true}"#.utf8) }
        let gate = Gate()
        let client = Self.holdingClient(hub, gate) { $0.method == "DELETE" }
        let m = InboxMutesModel(deps: .init(api: { client }, current: { Self.work }, show: { _ in }))
        await m.load()
        let mute = try #require(m.state.value?.first)
        let unmuting = Task { await m.unmute(mute) }
        await gate.holding()
        await m.load() // read while the unmute was on its way: the hub still had it
        #expect(m.state.value?.map(\.id) == ["m1"])
        await gate.open()
        await unmuting.value
        #expect(m.state.value?.isEmpty == true)
    }

    @Test func anotherAssistantCurrentReadsItsMutesAndShowsNoneOfTheOneBefore() async throws {
        let hub = Self.hub("/api/v1/inbox/mutes", "mutes", mine: Self.myMute, work: Self.workMute)
        var current = Self.work
        let m = InboxMutesModel(deps: .init(api: { hub.client }, current: { current }, show: { _ in }))
        await m.load()
        let before = m.readKey
        current = Self.home
        #expect(m.readKey != before)
        await m.load()
        #expect(m.state.value?.map(\.name) == ["example-org/one"])
    }

    @Test func aFailedLoadOvertakenByANewerOneSaysNothing() async throws {
        let hub = FakeHub()
        var answers = 0
        hub.on("GET", "/api/v1/inbox/mutes") { _ in
            answers += 1
            if answers == 2 { throw APIError(status: 503, message: "超时") } // the first load's, answered last
            return Data(#"{"mutes":[{"id":"m1","source":"github","tag":"repo","value":"example-org/one","name":"example-org/one"}]}"#.utf8)
        }
        let gate = Gate()
        let client = Self.holdingClient(hub, gate) { $0.method == "GET" }
        var shown: [String] = []
        let m = InboxMutesModel(deps: .init(api: { client }, current: { nil }, show: { shown.append("\($0)") }))
        let first = Task { await m.load() }
        await gate.holding()
        await m.load()
        await gate.open()
        await first.value
        #expect(m.state.value?.map(\.name) == ["example-org/one"])
        #expect(shown.isEmpty)
    }
}
