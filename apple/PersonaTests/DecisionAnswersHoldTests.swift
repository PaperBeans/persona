import Foundation
import Testing
@testable import Persona
import PersonaWire

/// The Needs You tray's decision row on V1 (#10465; `DecisionAnswers.Hold`): it holds an answer as the to-do's row does,
/// so the tray and the row answer one way on one device. Old hubs answer at once as before; the two-tap arm for
/// irreversible decisions stays; an answer elsewhere during the hold says so as the read shows it and sends nothing;
/// leaving the foreground sends it at once; a launch sends it again with the same op id, also one whose first send may
/// have reached the hub.
@MainActor
struct DecisionAnswersHoldTests {
    private func hub(_ rows: [String] = [TodoAnswersHarness.kyoto(), TodoAnswersHarness.plain]) -> FakeHub {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/todos", json: TodoAnswersHarness.list(rows, needsYou: 1))
        hub.on("GET", "/api/v1/todos/places", json: #"{"places":[{"id":"all","kind":"all","title":"","needs_you":1}],"counts":{}}"#)
        return hub
    }

    private func legacy(_ h: TodoAnswersHarness) throws -> TodoAsk { try #require(h.kyotoItem.openAsk) }

    /// V1: the tray's option is held ("You chose …", Undo), N drops at once, and once the window ends it goes through the
    /// asks route by the id the row's view lists, with the option.
    @Test func v1HoldsTheTraysAnswer() async throws {
        let hub = hub()
        let h = try await TodoAnswersHarness.make(hub)
        defer { h.store.forget() }
        let ask = try legacy(h)
        let path = TodoAnswersHarness.route("t01", TodoAnswersHarness.decision)
        hub.on("POST", path, json: #"{"ask":"\#(TodoAnswersHarness.decision)","outcome":"answered"}"#)
        await h.answers.choose(1, of: ask, todo: "t01", group: "g")
        #expect(h.answers.state(todo: "t01", ask: ask, group: "g") == .chosen(String(localized: "You chose \("Kaede") [To-dos Answers]")))
        #expect(h.answers.canUndo(todo: "t01", round: ask.askedAt, group: "g"))
        try await waitForAnswers { h.store.countsSource.needsYou == 0 }
        #expect(hub.sent("POST", path).isEmpty && hub.sent("POST", "/api/v1/todos/t01/accept").isEmpty)
        try await waitForAnswers { !hub.sent("POST", path).isEmpty }
        #expect(h.bodies(path).first == ["option": "Kaede"])
        try await waitForAnswers { !h.answers.canUndo(todo: "t01", round: ask.askedAt, group: "g") }
        #expect(h.answers.state(todo: "t01", ask: ask, group: "g")?.ended == true, "no button sends it again")
    }

    /// Before V1 (and on a hub without the view), the tray answers at once through the accept route, as before.
    @Test func oldHubsAnswerAtOnce() async throws {
        let hub = hub()
        let h = try await TodoAnswersHarness.make(hub, asks: false)
        defer { h.store.forget() }
        h.holds = false
        hub.on("POST", "/api/v1/todos/t01/accept", json: "{}")
        await h.answers.choose(0, of: try legacy(h), todo: "t01", group: "g")
        #expect(hub.sent("POST", "/api/v1/todos/t01/accept").count == 1, "sent before the call returns")
        #expect(h.answers.state(todo: "t01", ask: try legacy(h), group: "g") == .answered(String(localized: "Answered: \("Ryokan Hanami")")))
        #expect(h.outbox.entries.isEmpty)
    }

    /// An irreversible decision still takes two taps: the first arms the option, the second holds it.
    @Test func theArmRuleStays() async throws {
        let hub = hub([TodoAnswersHarness.kyoto().replacingOccurrences(of: #""outcome":"decide""#,
                                                                          with: #""outcome":"decide","why_owner":"irreversible""#),
                       TodoAnswersHarness.plain])
        let h = try await TodoAnswersHarness.make(hub)
        defer { h.store.forget() }
        let ask = try legacy(h)
        #expect(ask.needsConfirmation)
        await h.answers.choose(2, of: ask, todo: "t01", group: "g")
        #expect(h.answers.armedOption(todo: "t01", ask: ask, group: "g") == 2 && h.outbox.entries.isEmpty)
        await h.answers.choose(2, of: ask, todo: "t01", group: "g")
        #expect(h.outbox.entries.count == 1 && h.answers.armedOption(todo: "t01", ask: ask, group: "g") == nil)
    }

    /// The round ended elsewhere while the answer was held (a push lists the ask no more): nothing is sent, and the row
    /// says "Answered elsewhere" as that read lands, long before its window would end.
    @Test func anAnswerElsewhereDuringTheHoldSendsNothing() async throws {
        let hub = hub()
        let h = try await TodoAnswersHarness.make(hub, window: .seconds(30))
        defer { h.store.forget() }
        let ask = try legacy(h)
        await h.answers.choose(0, of: ask, todo: "t01", group: "g")
        hub.on("GET", "/api/v1/todos", json: TodoAnswersHarness.answered)
        await h.store.refresh(api: hub.client)
        try await waitForAnswers { h.answers.state(todo: "t01", ask: ask, group: "g") == .elsewhere }
        #expect(hub.sent("POST", TodoAnswersHarness.route("t01", TodoAnswersHarness.decision)).isEmpty)
        #expect(h.refusals.isEmpty, "not an error")
    }

    /// The scene leaves the foreground: a held answer goes at once, its window or not.
    @Test func leavingTheForegroundSendsItNow() async throws {
        let hub = hub()
        let h = try await TodoAnswersHarness.make(hub, window: .seconds(30))
        defer { h.store.forget() }
        let path = TodoAnswersHarness.route("t01", TodoAnswersHarness.decision)
        hub.on("POST", path, json: #"{"ask":"\#(TodoAnswersHarness.decision)","outcome":"answered"}"#)
        await h.answers.choose(0, of: try legacy(h), todo: "t01", group: "g")
        h.outbox.sendAllNow()
        try await waitForAnswers { !hub.sent("POST", path).isEmpty }
    }

    /// An answer the app was ended with goes once the hub answers a read, with the op id it was made with, so the
    /// hub's receipt answers it if the first one had reached it. Kept as the app was ended with it, before its window
    /// ended; the launch comes after that window (a launch inside it waits for a read after it, as every held write).
    @Test func aLaunchSendsItAgainWithTheSameOpID() async throws {
        let first = try await TodoAnswersHarness.make(hub(), window: .milliseconds(50))
        await first.answers.choose(0, of: try legacy(first), todo: "t01", group: "g")
        let kept = try #require(first.saved.first)
        first.store.forget()
        guard case .answer = kept.write else { Issue.record("not an answer"); return }
        #expect(!kept.sent, "kept unsent")
        try await Task.sleep(for: .milliseconds(150))

        let hub = hub()
        let path = TodoAnswersHarness.route("t01", TodoAnswersHarness.decision)
        hub.on("POST", path, json: #"{"ask":"\#(TodoAnswersHarness.decision)","outcome":"answered"}"#)
        let h = try await TodoAnswersHarness.make(hub, loading: [kept])
        defer { h.store.forget() }
        #expect(h.answers.state(todo: "t01", round: TodoAnswersHarness.asked, group: "g")?.ended == true,
                "it says what was chosen from the launch on")
        try await waitForAnswers { !hub.sent("POST", path).isEmpty }
        #expect(hub.sent("POST", path).first?.headers[OpRetry.header] == kept.id.uuidString.lowercased())
        #expect(h.store.countsSource.needsYou == 0, "it counts as answered all along")
    }

    /// An answer kept past `TodoOutbox.keepFor` without its hub: one that never left says it was not sent and its
    /// buttons come back; one whose send began may be with the Agent, so it says it is not sure and keeps the ask out of
    /// N, never saying it was not sent.
    @Test(arguments: [false, true])
    func anAnswerKeptTooLongSaysWhatBecameOfIt(sendBegan: Bool) async throws {
        let first = try await TodoAnswersHarness.make(hub(), window: .seconds(30))
        await first.answers.choose(0, of: try legacy(first), todo: "t01", group: "g")
        var kept = try #require(first.saved.first)
        first.store.forget()
        kept.made = Date().addingTimeInterval(-TodoOutbox.keepFor - 60)
        kept.sent = sendBegan

        let hub = hub()
        let h = try await TodoAnswersHarness.make(hub, loading: [kept])
        defer { h.store.forget() }
        try await waitForAnswers { h.outbox.entries.isEmpty }
        #expect(hub.sent("POST", TodoAnswersHarness.route("t01", TodoAnswersHarness.decision)).isEmpty, "never sent again")
        let state = h.answers.state(todo: "t01", round: TodoAnswersHarness.asked, group: "g")
        if sendBegan {
            #expect(state == .unsure)
            #expect(h.refusals.isEmpty, "never said as not sent")
            try await waitForAnswers { h.store.countsSource.needsYou == 0 } // the ask out of N until the view moves
        } else {
            #expect(state == nil, "its buttons come back")
            #expect(h.refusals.count == 1, "said once: \(h.refusals)")
            try await waitForAnswers { h.store.countsSource.needsYou == 1 } // the ask counts again
        }
    }

    /// An answer whose send began before the app was ended may have reached the hub: once a read shows its ask gone, it
    /// still goes again with its op id, and the receipt's answer settles it as chosen, never as answered elsewhere.
    @Test func aLaunchAfterASendThatMayHaveLandedAsksTheReceipt() async throws {
        let first = try await TodoAnswersHarness.make(hub(), window: .milliseconds(50))
        await first.answers.choose(0, of: try legacy(first), todo: "t01", group: "g")
        var kept = try #require(first.saved.first)
        first.store.forget()
        kept.sent = true
        try await Task.sleep(for: .milliseconds(150)) // the launch after its window, as `aLaunchSendsItAgainWithTheSameOpID`

        let hub = FakeHub()
        hub.on("GET", "/api/v1/todos", json: TodoAnswersHarness.answered)
        hub.on("GET", "/api/v1/todos/places", json: #"{"places":[{"id":"all","kind":"all","title":"","needs_you":1}],"counts":{}}"#)
        let path = TodoAnswersHarness.route("t01", TodoAnswersHarness.decision)
        hub.on("POST", path, json: #"{"ask":"\#(TodoAnswersHarness.decision)","outcome":"answered"}"#)
        let h = try await TodoAnswersHarness.make(hub, loading: [kept])
        defer { h.store.forget() }
        try await waitForAnswers { !hub.sent("POST", path).isEmpty }
        #expect(hub.sent("POST", path).first?.headers[OpRetry.header] == kept.id.uuidString.lowercased())
        try await waitForAnswers { h.outbox.entries.isEmpty }
        guard case .chosen? = h.answers.state(todo: "t01", round: TodoAnswersHarness.asked, group: "g") else {
            Issue.record("not settled as chosen"); return
        }
    }
}
