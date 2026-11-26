import Foundation
import Testing
@testable import Persona
import PersonaWire

/// A store, its outbox, the answers' state and the card's model wired as the app wires them (#10465), against a
/// `FakeHub`: what the tests of answering a to-do's asks share.
@MainActor
final class TodoAnswersHarness {
    let hub: FakeHub
    let store: TodoStore
    private let box: OutboxBox
    private(set) var outbox: TodoOutbox!
    private(set) var answers: DecisionAnswers!
    private(set) var model: AskCardModel!
    /// What the hub can do now (`todo_asks` on or off).
    var features: TodoFeatures {
        get { box.features }
        set { box.features = newValue }
    }
    /// While false, answers go at once as before V1 (`DecisionAnswers.Hold` is nil).
    var holds = true
    var toasts: [Toast] = []
    var refusals: [String] = []
    /// The to-dos declined through today's `decision-dismiss`.
    var declinedToday: [String] = []
    /// What the outbox last kept on disk.
    var saved: [TodoOutbox.Entry] = []

    static let asked = "2026-11-18T10:11:00Z"
    static let decision = "todo:decision@" + asked
    static let retry = "todo:retry@2026-11-18T09:58:00Z"

    /// A row in Needs you whose one ask is T01's decision, safe, recommending Ryokan Hanami; with `legacy`, today's
    /// `ask` beside it, as a hub with the view sends both.
    static func kyoto(legacy: Bool = true) -> String {
        let ask = #"{"id":"\#(decision)","kind":"decision","home":"todo","round":"\#(asked)","text":"Which ryokan?","#
            + #""recommend":"Ryokan Hanami","options":[{"label":"Ryokan Hanami","leads_to":"¥58k a night"},{"label":"Kaede"},"#
            + #"{"label":"Tsuki"}],"default_at":"2026-11-20T09:00:00Z","risk":"safe","lockscreen":"answer"}"#
        let view = #"{"phase":"needs_you","section":"needs_you","reason":"owner_decides","line":{"key":"decision","#
            + #""args":{"text":"Which ryokan?","recommend":"Ryokan Hanami"}},"rank":0,"asks":[\#(ask)]}"#
        let old = #""ask":{"outcome":"decide","verdict":"v","recommend":"Ryokan Hanami","asked_at":"\#(asked)","question":"Which ryokan?","#
            + #""options":[{"label":"Ryokan Hanami"},{"label":"Kaede"},{"label":"Tsuki"}]}"#
        return TodoViewJSON.row("t01", title: "Plan the Kyoto trip", extra: legacy ? old : "", view: view)
    }

    /// T03's failed start, safe to start again.
    static let photos = TodoViewJSON.row("t03", title: "Sort last year’s photos", extra: #""origin":"c1""#, view:
        #"{"phase":"needs_you","section":"needs_you","reason":"failed_start","line":{"key":"failed_start","args":{"error":"Desk Mac is offline"}},"#
        + #""rank":1,"asks":[{"id":"\#(retry)","kind":"retry","home":"todo","risk":"safe","lockscreen":"answer"}]}"#)

    static let plain = TodoViewJSON.row("t11", title: "Renew passport", view: TodoViewJSON.view(TodoListSection.todo, line: "rest"))

    static func list(_ rows: [String], needsYou: Int) -> String {
        TodoViewJSON.list(rows, counts: TodoViewJSON.counts(needsYou: needsYou, open: needsYou + 1,
                                                            sections: [TodoListSection.needsYou: needsYou, TodoListSection.todo: 1]))
    }

    /// The hub after the answer: the decision taken, the row Working.
    static let answered = list([TodoViewJSON.row("t01", title: "Plan the Kyoto trip", view: TodoViewJSON.view(TodoListSection.working)),
                                photos, plain], needsYou: 1)

    /// The asks route's path for `ask` of to-do `todo`, as the client writes it.
    static func route(_ todo: String, _ ask: String) -> String {
        "/api/v1/todos/\(APIClient.todoPath(todo))/asks/\(APIClient.todoPath(ask))/answer"
    }

    private init(hub: FakeHub, features: TodoFeatures) {
        self.hub = hub
        let box = OutboxBox(features: features)
        self.box = box
        // The store reads the hub's features at each write, so a test may change them between sends.
        var v1 = TodoStore.V1.on()
        v1.features = { box.features }
        v1.pending = { box.outbox?.pending ?? [:] }
        v1.answering = { box.outbox?.answering ?? [:] }
        v1.answered = { box.outbox?.hubAnswered(read: $0) }
        store = TodoStore(cachedTodos: [], cacheWriter: nil, hubListChanged: {}, v1: v1)
    }

    /// Wired, the list read once. `asks`: the hub lists `todo_asks`. `window`: how long an answer waits for Undo.
    /// `loading`: what the outbox finds kept on disk, as at a launch.
    static func make(_ hub: FakeHub, asks: Bool = true, window: Duration = .milliseconds(300),
                     loading: [TodoOutbox.Entry] = []) async throws -> TodoAnswersHarness {
        let h = TodoAnswersHarness(hub: hub, features: TodoFeatures(view: true, places: true, asks: asks))
        let store = h.store
        h.outbox = TodoOutbox(deps: .init(
            load: { loading },
            save: { [weak h] entries, _ in h?.saved = entries; return Task {} },
            hub: { "g" },
            send: { entry, answered in await store.send(entry, answered: answered, api: hub.client) },
            refused: { [weak h] _, message, _ in h?.refusals.append(message) },
            settled: { [weak h] entry, sent in h?.answers.settled(entry, sent) },
            standing: { id in
                store.byID[id].map { item in
                    .init(title: item.title, updatedAt: item.updatedAt, done: item.isDone, someday: false,
                          asks: store.views[id].map { Set($0.asks?.map(\.id) ?? []) })
                }
            },
            listed: { store.todos }))
        h.box.outbox = h.outbox
        h.answers = DecisionAnswers(api: { _ in hub.client }, failed: { [weak h] in h?.refusals.append($0) },
                                    taken: { store.reload(api: hub.client) },
                                    hold: { [weak h] in
                                        guard let h, h.holds else { return nil }
                                        return DecisionAnswers.Hold(outbox: h.outbox, counts: store.countsSource,
                                                                    askID: { store.askID(todo: $0, round: $1) },
                                                                    manager: { store.byID[$0]?.manager }, window: { window },
                                                                    toast: { [weak h] in h?.toasts.append($0) },
                                                                    respond: { try await store.respond(todo: $0, round: $1, response: $2,
                                                                                                       api: hub.client) })
                                    })
        h.model = AskCardModel(deps: AskCardDeps(answers: h.answers, store: store, api: { hub.client }, group: { "g" },
                                                 features: { [weak h] in h?.features ?? TodoFeatures() },
                                                 toast: { [weak h] in h?.toasts.append($0) },
                                                 declineToday: { [weak h] in h?.declinedToday.append($0.id); return true }))
        await store.refresh(api: hub.client)
        // What was kept on disk is in once the hub has answered a read (the store tells the outbox).
        await h.outbox.ready()
        try await waitForAnswers { store.viewsRead && !store.views.isEmpty }
        return h
    }

    var kyotoAsk: OwnerAsk { store.views["t01"]!.asks!.first! }
    var kyotoItem: TodoItem { store.byID["t01"]! }

    /// The bodies the hub was sent on `path`, as objects.
    func bodies(_ path: String, method: String = "POST") -> [[String: String]] {
        hub.sent(method, path).compactMap { req in
            req.body.flatMap { try? JSONSerialization.jsonObject(with: $0) as? [String: String] }
        }
    }
}

/// What the store's closures read that is made after it (the outbox) or changes under it (the hub's features).
@MainActor final class OutboxBox {
    var outbox: TodoOutbox?
    var features: TodoFeatures
    init(features: TodoFeatures) { self.features = features }
}

/// Waits (up to 5 s) for something an answer does.
@MainActor
func waitForAnswers(_ condition: @MainActor () -> Bool) async throws {
    for _ in 0..<250 where !condition() { try await Task.sleep(for: .milliseconds(20)) }
    try #require(condition(), "it did not happen in time")
}

/// Answering a to-do's own asks from its row and its card (#10465; `AskCardModel`): OK and Retry are held through their
/// Undo window and then go through the asks route with their op id, or today's accept and retry routes on a hub without
/// `todo_asks` (a step's ask on the step); N drops at the tap; an ask answered elsewhere says so, and one nobody can say
/// reached the Agent says that and is never sent again; a refusal is said and the buttons come back; a row or view
/// missing at send time sends all the same; Later and Don't handle; the OK's words and the card's options.
@MainActor
struct AskCardModelTests {
    private func hub(_ rows: [String] = [TodoAnswersHarness.kyoto(), TodoAnswersHarness.photos, TodoAnswersHarness.plain],
                     needsYou: Int = 2) -> FakeHub {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/todos", json: TodoAnswersHarness.list(rows, needsYou: needsYou))
        hub.on("GET", "/api/v1/todos/places", json: #"{"places":[{"id":"all","kind":"all","title":"","needs_you":2}],"counts":{}}"#)
        return hub
    }

    /// OK on a safe decision: "✓ You chose … · Undo" and N down by one at the tap, nothing sent within the window, then
    /// the asks route with its op id and the recommendation; the Undo goes with the window.
    @Test func okIsHeldThenGoesThroughTheAsksRoute() async throws {
        let hub = hub()
        let h = try await TodoAnswersHarness.make(hub)
        defer { h.store.forget() }
        let ask = h.kyotoAsk, item = h.kyotoItem // read once: the view moves on as the hub answers
        let path = TodoAnswersHarness.route("t01", TodoAnswersHarness.decision)
        hub.on("POST", path) { _ in
            hub.on("GET", "/api/v1/todos", json: TodoAnswersHarness.answered)
            return Data(#"{"ask":"\#(TodoAnswersHarness.decision)","outcome":"answered"}"#.utf8)
        }
        #expect(h.store.countsSource.needsYou == 2)
        await h.model.ok(ask, todo: item)
        guard case .chosen(let label, let undo)? = h.model.mark(ask, todo: "t01") else {
            Issue.record("no chosen mark"); return
        }
        #expect(label == String(localized: "You chose \("Ryokan Hanami") [To-dos Answers]") && undo)
        try await waitForAnswers { h.store.countsSource.needsYou == 1 }
        #expect(h.store.countsSource.counts.sections[TodoListSection.needsYou] == 1, "the header with it")
        #expect(hub.sent("POST", path).isEmpty, "nothing goes within the window")
        try await waitForAnswers { !hub.sent("POST", path).isEmpty }
        let sent = try #require(hub.sent("POST", path).first)
        #expect(sent.headers[OpRetry.header] != nil, "with its op id")
        #expect(h.bodies(path).first == ["option": "Ryokan Hanami"])
        try await waitForAnswers { h.store.views["t01"]?.section == TodoListSection.working }
        #expect(h.store.countsSource.needsYou == 1, "the hub's number, nothing taken off twice")
        #expect(hub.sent("POST", "/api/v1/todos/t01/accept").isEmpty)
    }

    /// A hub without `todo_asks`: the same hold, then today's accept route with the round and the option.
    @Test func withoutTodoAsksTheAcceptRouteAnswers() async throws {
        let hub = hub()
        let h = try await TodoAnswersHarness.make(hub, asks: false)
        defer { h.store.forget() }
        let ask = h.kyotoAsk, item = h.kyotoItem // read once: the view moves on as the hub answers
        hub.on("POST", "/api/v1/todos/t01/accept") { _ in
            hub.on("GET", "/api/v1/todos", json: TodoAnswersHarness.answered)
            return Data("{}".utf8)
        }
        await h.model.choose("Kaede", of: ask, todo: item)
        try await waitForAnswers { !hub.sent("POST", "/api/v1/todos/t01/accept").isEmpty }
        #expect(h.bodies("/api/v1/todos/t01/accept").first == ["asked_at": TodoAnswersHarness.asked, "option": "Kaede"])
        #expect(hub.sent("POST", TodoAnswersHarness.route("t01", TodoAnswersHarness.decision)).isEmpty)
    }

    /// A manager that reconnected at an older version answers the asks route 405: today's route answers instead.
    @Test func aRouteTheManagerLacksFallsBackToToday() async throws {
        let hub = hub()
        let h = try await TodoAnswersHarness.make(hub)
        defer { h.store.forget() }
        let ask = h.kyotoAsk, item = h.kyotoItem // read once: the view moves on as the hub answers
        hub.on("POST", TodoAnswersHarness.route("t01", TodoAnswersHarness.decision)) { _ in
            throw APIError(status: 405, message: "method not allowed")
        }
        hub.on("POST", "/api/v1/todos/t01/accept", json: "{}")
        await h.model.ok(ask, todo: item)
        try await waitForAnswers { !hub.sent("POST", "/api/v1/todos/t01/accept").isEmpty }
        #expect(h.bodies("/api/v1/todos/t01/accept").first?["option"] == "Ryokan Hanami")
        #expect(h.refusals.isEmpty)
    }

    /// Undo within the window: nothing goes, the mark goes, N is back.
    @Test func undoWithinTheWindowSendsNothing() async throws {
        let hub = hub()
        let h = try await TodoAnswersHarness.make(hub)
        defer { h.store.forget() }
        let ask = h.kyotoAsk, item = h.kyotoItem // read once: the view moves on as the hub answers
        await h.model.ok(ask, todo: item)
        try await waitForAnswers { h.store.countsSource.needsYou == 1 }
        h.model.undo(ask, todo: item)
        #expect(h.model.mark(ask, todo: "t01") == nil)
        try await waitForAnswers { h.store.countsSource.needsYou == 2 }
        try await Task.sleep(for: .milliseconds(500)) // past the window: a proof that nothing went needs it to have passed
        #expect(hub.sent("POST", TodoAnswersHarness.route("t01", TodoAnswersHarness.decision)).isEmpty && h.outbox.entries.isEmpty)
    }

    /// The round ended elsewhere (409 `ask_stale`): "Answered elsewhere", no refusal said, and the list read again, which
    /// files the row where the hub now has it.
    @Test func anAskAnsweredElsewhereRedrawsTheRow() async throws {
        let hub = hub()
        let h = try await TodoAnswersHarness.make(hub)
        defer { h.store.forget() }
        let ask = h.kyotoAsk, item = h.kyotoItem // read once: the view moves on as the hub answers
        hub.on("POST", TodoAnswersHarness.route("t01", TodoAnswersHarness.decision)) { _ in
            hub.on("GET", "/api/v1/todos", json: TodoAnswersHarness.answered)
            throw APIError(status: 409, message: "This was already handled", fields: ["code": "ask_stale"])
        }
        let reads = hub.sent("GET", "/api/v1/todos").count
        await h.model.ok(ask, todo: item)
        try await waitForAnswers { h.model.mark(ask, todo: "t01") == .elsewhere }
        #expect(h.refusals.isEmpty && h.toasts.isEmpty, "said in its place, not as an error")
        try await waitForAnswers { hub.sent("GET", "/api/v1/todos").count > reads }
        try await waitForAnswers { h.store.views["t01"]?.section == TodoListSection.working }
        #expect(h.store.countsSource.needsYou == 1)
    }

    /// A node's refusal (423): dropped and said, the buttons back, N back.
    @Test func aRefusalIsSaidAndTheAnswerComesBack() async throws {
        let hub = hub()
        let h = try await TodoAnswersHarness.make(hub)
        defer { h.store.forget() }
        let ask = h.kyotoAsk, item = h.kyotoItem // read once: the view moves on as the hub answers
        hub.on("POST", TodoAnswersHarness.route("t01", TodoAnswersHarness.decision)) { _ in
            throw APIError(status: 423, message: "The computer is locked", fields: ["code": "node_locked", "delivered": "none"])
        }
        await h.model.ok(ask, todo: item)
        try await waitForAnswers { !h.refusals.isEmpty }
        #expect(h.refusals.first == "The computer is locked")
        try await waitForAnswers { h.model.mark(ask, todo: "t01") == nil && h.store.countsSource.needsYou == 2 }
        #expect(h.outbox.entries.isEmpty)
    }

    /// The hub could not say whether the answer reached the Agent (502 `send_in_doubt`, `delivered: "unknown"`): the row
    /// says it is not sure, nothing is sent again (the receipt would say the same), the buttons stay away, and the ask
    /// stays out of N until the view says.
    @Test func anAnswerInDoubtIsNeverSentAgain() async throws {
        let hub = hub()
        let h = try await TodoAnswersHarness.make(hub)
        defer { h.store.forget() }
        let ask = h.kyotoAsk, item = h.kyotoItem
        let path = TodoAnswersHarness.route("t01", TodoAnswersHarness.decision)
        hub.on("POST", path) { _ in
            throw APIError(status: 502, message: "The answer may have reached the Agent",
                           fields: ["code": "send_in_doubt", "delivered": "unknown"])
        }
        await h.model.ok(ask, todo: item)
        try await waitForAnswers { h.model.mark(ask, todo: "t01") == .unsure }
        #expect(h.refusals.isEmpty, "never said as not sent")
        await h.store.refresh(api: hub.client)
        try await Task.sleep(for: .milliseconds(400))
        #expect(hub.sent("POST", path).count == 1, "never sent again")
        #expect(h.outbox.entries.isEmpty && h.store.countsSource.needsYou == 1)
    }

    /// A read during the hold that lists the row without its view (a manager reconnecting), or not at all, says nothing
    /// of the ask: the answer still goes when its window ends, and the hub's answer says where it stands.
    @Test func anAnswerGoesWhenTheRowOrItsViewIsMissing() async throws {
        let without = [TodoAnswersHarness.list([TodoViewJSON.row("t01", title: "Plan the Kyoto trip"), TodoAnswersHarness.photos,
                                                TodoAnswersHarness.plain], needsYou: 1),
                       TodoAnswersHarness.list([TodoAnswersHarness.photos, TodoAnswersHarness.plain], needsYou: 1)]
        for list in without {
            let hub = hub()
            let h = try await TodoAnswersHarness.make(hub, window: .milliseconds(500))
            let ask = h.kyotoAsk, item = h.kyotoItem
            let path = TodoAnswersHarness.route("t01", TodoAnswersHarness.decision)
            hub.on("POST", path, json: #"{"ask":"\#(TodoAnswersHarness.decision)","outcome":"answered"}"#)
            await h.model.ok(ask, todo: item)
            hub.on("GET", "/api/v1/todos", json: list)
            await h.store.refresh(api: hub.client)
            try await waitForAnswers { !hub.sent("POST", path).isEmpty }
            try await waitForAnswers { h.outbox.entries.isEmpty }
            guard case .chosen? = h.answers.state(todo: "t01", round: TodoAnswersHarness.asked, group: "g") else {
                Issue.record("not taken: \(String(describing: h.answers.state(todo: "t01", round: TodoAnswersHarness.asked, group: "g")))")
                h.store.forget()
                return
            }
            h.store.forget()
        }
    }

    /// A step's decision lifted onto its parent's row, on a hub without `todo_asks`: today's accept route on the step,
    /// with the step's round; kept on the device by the step's round, as the tray keeps it.
    @Test func aStepsAskGoesToTheStepThroughTodaysRoute() async throws {
        let round = "2026-11-18T09:40:00Z"
        let stepAsk = "step:s1/todo:decision@" + round
        let parent = TodoViewJSON.row("p1", title: "Move flat", view:
            #"{"phase":"needs_you","section":"needs_you","reason":"owner_decides","line":{"key":"decision","args":{"text":"Which van?"}},"#
            + #""rank":0,"asks":[{"id":"\#(stepAsk)","kind":"decision","home":"todo","round":"\#(round)","text":"Which van?","#
            + #""recommend":"Small van","options":[{"label":"Small van"},{"label":"Large van"}],"risk":"safe"}]}"#)
        let hub = hub([parent, TodoAnswersHarness.plain], needsYou: 1)
        let h = try await TodoAnswersHarness.make(hub, asks: false)
        defer { h.store.forget() }
        let ask = try #require(h.store.views["p1"]?.asks?.first)
        let item = try #require(h.store.byID["p1"])
        #expect(ask.step == "s1")
        hub.on("POST", "/api/v1/todos/s1/accept", json: "{}")
        await h.model.ok(ask, todo: item)
        #expect(h.answers.canUndo(todo: "s1", round: round, group: "g"), "kept by the step's round")
        try await waitForAnswers { !hub.sent("POST", "/api/v1/todos/s1/accept").isEmpty }
        #expect(h.bodies("/api/v1/todos/s1/accept").first == ["asked_at": round, "option": "Small van"])
        #expect(hub.sent("POST", "/api/v1/todos/p1/accept").isEmpty, "never on the parent")
    }

    /// Two answers held on one to-do (its own decision and a step's) each keep their own Undo.
    @Test func twoAnswersHeldOnOneToDoEachKeepTheirUndo() async throws {
        let round = "2026-11-18T09:40:00Z"
        let own = #"{"id":"todo:decision@\#(TodoAnswersHarness.asked)","kind":"decision","home":"todo","round":"\#(TodoAnswersHarness.asked)","#
            + #""recommend":"A","options":[{"label":"A"},{"label":"B"}],"risk":"safe"}"#
        let step = #"{"id":"step:s1/todo:decision@\#(round)","kind":"decision","home":"todo","round":"\#(round)","#
            + #""recommend":"C","options":[{"label":"C"},{"label":"D"}],"risk":"safe"}"#
        let parent = TodoViewJSON.row("p1", title: "Move flat", view:
            #"{"phase":"needs_you","section":"needs_you","reason":"owner_decides","line":{"key":"decision"},"rank":0,"asks":[\#(own),\#(step)]}"#)
        let hub = hub([parent, TodoAnswersHarness.plain], needsYou: 1)
        let h = try await TodoAnswersHarness.make(hub, window: .seconds(30))
        defer { h.store.forget() }
        let asks = try #require(h.store.views["p1"]?.asks)
        let item = try #require(h.store.byID["p1"])
        await h.model.ok(asks[0], todo: item)
        await h.model.ok(asks[1], todo: item)
        #expect(h.outbox.entries.count == 2)
        #expect(h.model.mark(asks[0], todo: "p1") == .chosen(String(localized: "You chose \("A") [To-dos Answers]"), undo: true))
        #expect(h.model.mark(asks[1], todo: "p1") == .chosen(String(localized: "You chose \("C") [To-dos Answers]"), undo: true))
        h.model.undo(asks[0], todo: item)
        #expect(h.model.mark(asks[0], todo: "p1") == nil && h.outbox.entries.count == 1, "the other still held")
        #expect(h.model.mark(asks[1], todo: "p1") != nil)
    }

    /// A manager without the asks route answers Don't handle with a 405: today's `decision-dismiss` takes it instead.
    @Test func declineFallsBackToTodayWhenTheRouteIsMissing() async throws {
        let hub = hub()
        let h = try await TodoAnswersHarness.make(hub)
        defer { h.store.forget() }
        let ask = h.kyotoAsk, item = h.kyotoItem
        hub.on("POST", TodoAnswersHarness.route("t01", TodoAnswersHarness.decision)) { _ in
            throw APIError(status: 405, message: "method not allowed")
        }
        await h.model.decline(ask, todo: item)
        #expect(h.declinedToday == ["t01"] && h.toasts.isEmpty)
        #expect(h.store.countsSource.needsYou == 1)
    }

    /// The tray's fixed answers on V1 go at once through the asks route while the hub lists it; without it, the accept
    /// route as before.
    @Test func theTraysFixedAnswersGoThroughTheAsksRoute() async throws {
        for asks in [true, false] {
            let hub = hub()
            let h = try await TodoAnswersHarness.make(hub, asks: asks)
            let path = TodoAnswersHarness.route("t01", TodoAnswersHarness.decision)
            hub.on("POST", path, json: #"{"ask":"\#(TodoAnswersHarness.decision)","outcome":"answered"}"#)
            hub.on("POST", "/api/v1/todos/t01/accept", json: "{}")
            let ask = try #require(h.kyotoItem.openAsk)
            await h.answers.respond(.decideYourself, to: ask, todo: "t01", group: "g")
            if asks {
                #expect(h.bodies(path).first == ["response": TodoAsk.Response.decideYourself.rawValue])
                #expect(hub.sent("POST", path).first?.headers[OpRetry.header] != nil, "with an op id")
                #expect(hub.sent("POST", "/api/v1/todos/t01/accept").isEmpty)
            } else {
                #expect(h.bodies("/api/v1/todos/t01/accept").first?["response"] == TodoAsk.Response.decideYourself.rawValue)
            }
            #expect(h.answers.state(todo: "t01", ask: ask, group: "g") == .sentBack)
            h.store.forget()
        }
    }

    /// Retry on a failed start: held, then the asks route with no answer in its body; on a hub without `todo_asks`,
    /// today's retry route.
    @Test func retryIsHeldThenStartsItAgain() async throws {
        for asks in [true, false] {
            let hub = hub()
            let h = try await TodoAnswersHarness.make(hub, asks: asks)
            let ask = try #require(h.store.views["t03"]?.asks?.first)
            let item = try #require(h.store.byID["t03"])
            let path = asks ? TodoAnswersHarness.route("t03", TodoAnswersHarness.retry) : "/api/v1/todos/t03/retry"
            hub.on("POST", path, json: asks ? #"{"ask":"\#(TodoAnswersHarness.retry)","outcome":"answered"}"# : #"{"todo":{"id":"t03","title":"x"}}"#)
            await h.model.retry(ask, todo: item)
            guard case .chosen? = h.model.mark(ask, todo: "t03") else { Issue.record("no chosen mark"); return }
            #expect(hub.sent("POST", path).isEmpty)
            try await waitForAnswers { !hub.sent("POST", path).isEmpty }
            if asks { #expect(h.bodies(path).first == [:], "no answer: the ask's one action") }
            h.store.forget()
        }
    }

    /// Later: nothing sent, the ask still counts everywhere (the tray keeps it too: one number); brought back, it is open
    /// again.
    @Test func laterPutsTheAskOffForThisSession() async throws {
        let hub = hub()
        let h = try await TodoAnswersHarness.make(hub)
        defer { h.store.forget() }
        let ask = h.kyotoAsk, item = h.kyotoItem // read once: the view moves on as the hub answers
        h.model.putOff(ask, todo: item)
        #expect(h.model.mark(ask, todo: "t01") == .later)
        #expect(h.store.countsSource.needsYou == 2, "still counted")
        try await Task.sleep(for: .milliseconds(400))
        #expect(hub.requests.allSatisfy { $0.method == "GET" }, "nothing written")
        h.model.bringBack(ask, todo: item)
        #expect(h.model.mark(ask, todo: "t01") == nil)
    }

    /// Don't handle: sent at once through the asks route, N down, a toast whose Undo sends the receipt back.
    @Test func declineGoesAtOnceAndItsUndoTakesItBack() async throws {
        let hub = hub()
        let h = try await TodoAnswersHarness.make(hub)
        defer { h.store.forget() }
        let ask = h.kyotoAsk, item = h.kyotoItem // read once: the view moves on as the hub answers
        let path = TodoAnswersHarness.route("t01", TodoAnswersHarness.decision)
        hub.on("POST", path) { req in
            let body = (try? JSONSerialization.jsonObject(with: req.body ?? Data()) as? [String: String]) ?? [:]
            return Data((body["outcome"] == "undo" ? #"{"ask":"a","outcome":"undone"}"# : #"{"ask":"a","outcome":"declined","receipt":"dsm_1"}"#).utf8)
        }
        #expect(h.model.declinable(ask, todo: item))
        await h.model.decline(ask, todo: item)
        #expect(h.bodies(path).first == ["outcome": "decline"])
        #expect(h.store.countsSource.needsYou == 1)
        let undo = try #require(h.toasts.last?.action)
        undo.run()
        try await waitForAnswers { h.bodies(path).count == 2 }
        #expect(h.bodies(path).last == ["outcome": "undo", "receipt": "dsm_1"])
        try await waitForAnswers { h.store.countsSource.needsYou == 2 }
    }

    /// "OK: ‹option›" while it fits 260 pt at the text size, else "OK": from its length and the scale, never measured.
    @Test func theOKSaysOKWhenItsWordsWouldNotFit() throws {
        let view = TodoSummary(phase: TodoPhase.needsYou, section: TodoListSection.needsYou, reason: TodoReason.ownerDecides,
                               asks: [OwnerAsk(id: "a", kind: OwnerAskKind.decision, home: OwnerAskHome.todo, round: "r", recommend: "Ryokan Hanami",
                                               options: [.init(label: "Ryokan Hanami"), .init(label: "Kaede")], risk: OwnerAskRisk.safe)])
        let answers = try #require(TodoRowAnswersLogic.answers(view, section: TodoListSection.needsYou, closed: false, mark: nil))
        #expect(answers.primary == .ok(option: "Ryokan Hanami") && answers.options)
        #expect(answers.okTitle(scale: 1) == TodoRowAnswers.okLong("Ryokan Hanami"))
        #expect(answers.okTitle(scale: 3) == String(localized: "OK [To-dos Answers]"), "the largest sizes")
        var long = answers
        long.primary = .ok(option: String(repeating: "Ryokan ", count: 6))
        long.okWidth = TodoRowAnswers.width(TodoRowAnswers.okLong(String(repeating: "Ryokan ", count: 6)))
        #expect(long.okTitle(scale: 1) == String(localized: "OK [To-dos Answers]"))
        #expect(TodoRowAnswers.width("京都旅馆") > TodoRowAnswers.width("abcd"), "a wide character is an em")
        // An open decision, an ask without a risk (an older manager's), and a plan offer nothing on the row.
        let quiet: [(String, String?)] = [(OwnerAskKind.decision, OwnerAskRisk.open), (OwnerAskKind.decision, nil),
                                          (OwnerAskKind.plan, OwnerAskRisk.open)]
        for (kind, risk) in quiet {
            var other = view
            other.asks?[0].kind = kind
            other.asks?[0].risk = risk
            #expect(TodoRowAnswersLogic.answers(other, section: TodoListSection.needsYou, closed: false, mark: nil) == nil)
        }
        #expect(TodoRowAnswersLogic.answers(view, section: TodoListSection.working, closed: false, mark: nil) == nil,
                "only where it needs the owner")
    }

    /// A run's ask offers nothing on the row, a safe question with its card included: its home is its session, and the
    /// row's tap opens the to-do, where it is. A failed start offers Retry and Options; nothing on a closed row.
    @Test func aRunsAskOffersNothingOnTheRow() throws {
        let run = TodoRunRef(machineId: MachineID("macbook"), sessionId: SessionID("s1"), title: "Widget")
        let question = OwnerAsk(id: "run:macbook/s1@x", kind: OwnerAskKind.question, home: OwnerAskHome.run, text: "Which icon set?",
                                run: run, risk: OwnerAskRisk.safe, interaction: InteractionID("ix1"))
        let view = { (ask: OwnerAsk) in
            TodoSummary(phase: TodoPhase.needsYou, section: TodoListSection.needsYou, reason: TodoReason.agentAsks, asks: [ask])
        }
        #expect(TodoRowAnswersLogic.answers(view(question), section: TodoListSection.needsYou, closed: false, mark: nil) == nil)
        let retry = OwnerAsk(id: "todo:retry@x", kind: OwnerAskKind.retry, home: OwnerAskHome.todo, risk: OwnerAskRisk.safe)
        let started = try #require(TodoRowAnswersLogic.answers(view(retry), section: TodoListSection.needsYou, closed: false, mark: nil))
        #expect(started.primary == .retry && started.options)
        #expect(TodoRowAnswersLogic.answers(view(retry), section: TodoListSection.needsYou, closed: true, mark: nil) == nil,
                "nothing on a closed row")
    }

    /// A decision without a recommendation offers Options alone, its card draws every option alike, two to a row, and
    /// says "Persona decides …", never taking the first option for the recommended one; with one, the others, a fourth
    /// included, are all on the card.
    @Test func everyOptionIsOnTheCardAndOnlyTheHubsRecommendationIsMarked() throws {
        let labels = ["Ryokan Hanami", "Kaede", "Tsuki", "Hoshi"]
        var ask = OwnerAsk(id: "a", kind: OwnerAskKind.decision, home: OwnerAskHome.todo, round: "r", recommend: "Ryokan Hanami",
                           options: labels.map { .init(label: $0) }, risk: OwnerAskRisk.safe)
        #expect(AskCard.optionRows(ask) == [["Kaede", "Tsuki"], ["Hoshi"]], "the fourth too")
        ask.recommend = nil
        #expect(ask.recommendedOption == nil)
        #expect(AskCard.optionRows(ask) == [["Ryokan Hanami", "Kaede"], ["Tsuki", "Hoshi"]])
        let view = TodoSummary(phase: TodoPhase.needsYou, section: TodoListSection.needsYou, reason: TodoReason.ownerDecides, asks: [ask])
        let answers = try #require(TodoRowAnswersLogic.answers(view, section: TodoListSection.needsYou, closed: false, mark: nil))
        #expect(answers.primary == nil && answers.options)
        ask.recommend = "Somewhere else"
        #expect(ask.recommendedOption == nil, "a recommendation that is none of the options")
    }

    /// The card says what every option leads to, and what happens when nobody answers, as two paragraphs; kept per
    /// to-do, so two to-dos asked at the same moment never share theirs.
    @Test func theCardSaysWhatEachOptionLeadsTo() async throws {
        let hub = hub()
        let h = try await TodoAnswersHarness.make(hub)
        defer { h.store.forget() }
        var ask = h.kyotoAsk
        ask.options = [.init(label: "Ryokan Hanami", leadsTo: "¥58k a night"), .init(label: "Kaede", leadsTo: "¥52k a night")]
        let now = try #require(JSON.parseDate("2026-11-18T10:14:00Z"))
        let about = h.model.about(ask, todo: "t01", now: now)
        #expect(about.count == 2)
        #expect(about.first == String(localized: "\("Ryokan Hanami"): \("¥58k a night") [To-dos Answers]") + TodoTimeWords.separator
                + String(localized: "\("Kaede"): \("¥52k a night") [To-dos Answers]"))
        var other = ask
        other.options = [.init(label: "Tea", leadsTo: "calm")]
        #expect(h.model.about(other, todo: "t02", now: now).first != about.first, "kept per to-do")
    }

    /// A safe decision's OK estimates its width generously for capitals, so a label in capitals says "OK" sooner.
    @Test func capitalsCountWider() {
        #expect(TodoRowAnswers.width("RYOKAN HANAMI") > TodoRowAnswers.width("ryokan hanami"))
        #expect(TodoRowAnswers.width("WWW") > TodoRowAnswers.width("AAA"))
    }

    /// A retry's round is its id, which is never parsed as a time: the key keeps it as it is.
    @Test func aRoundThatIsNoTimeIsNeverParsed() {
        #expect(DecisionAnswers.key(todo: "t03", round: TodoAnswersHarness.retry, group: "g") == "g|t03|" + TodoAnswersHarness.retry)
        let a = DecisionAnswers.key(todo: "t01", round: "2026-11-18T10:11:00Z", group: "g")
        #expect(a == DecisionAnswers.key(todo: "t01", round: "2026-11-18T10:11:00.000Z", group: "g"), "however the hub spells it")
    }
}
