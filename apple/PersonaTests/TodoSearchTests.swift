import Foundation
import SwiftUI
import Testing
@testable import Persona
import PersonaWire

/// The To-dos list's search (#10465; DESIGN_SYSTEM 3.4 "To-dos list", "List controls"): the titles and notes of every
/// to-do of the current assistant the device holds, open and closed; a title match keeps the row's line 2, a notes match
/// says the sentence that matched with its words on the accent tint ("List controls"); results offer no Mark Done and no
/// answers, while one found by its title still says an answer given here; the model looks off the main actor, a newer
/// key taking over, a cleared field is the list again at once, and a push while it searches looks through the to-dos it
/// changed.
struct TodoSearchTests {
    private let now = Date(timeIntervalSince1970: 1_800_000_000)
    private let words = TodoWords(locale: Locale(identifier: "en_US"))

    private func filed(_ id: String, _ section: TodoListSection) -> TodoFiled {
        TodoFiled(id: id, manager: nil, section: section, closed: section == TodoListSection.done, rank: 0, inactive: false,
                  byView: true, quiet: false, partOf: nil, project: nil, asks: [])
    }

    /// The title first, so a row found by its name keeps its own line 2; else the sentence of the notes that holds the
    /// words, on one line, every match in it marked (UTF-16, as the chat search's hits); case, accents and width aside.
    @Test func aTitleThenTheSentenceOfTheNotes() throws {
        #expect(TodoSearch.hit(TodoItem(id: "a", title: "Plan the Kyoto trip", notes: "Kyoto in April"), words: "kyoto")
                == TodoSearchHit(), "found by its name")
        let item = TodoItem(id: "b", title: "Book a room", notes: "Two of us.\nRyokan near Gion, under ¥60k a night. Call first.")
        let found = try #require(TodoSearch.hit(item, words: "gion")?.found)
        #expect(found.text == "Ryokan near Gion, under ¥60k a night.")
        #expect(found.marks == [[12, 16]])
        #expect(TodoSearch.hit(item, words: "passport") == nil)
        // Accents and case aside; every match in the sentence, and only that sentence.
        let accents = try #require(TodoSearch.sentence(in: "Kyōto first, then kyoto again. Not here.", matching: "KYOTO"))
        #expect(accents.text == "Kyōto first, then kyoto again.")
        #expect(accents.marks == [[0, 5], [18, 23]])
        // Offsets in UTF-16, past a character outside the basic plane.
        let emoji = try #require(TodoSearch.sentence(in: "🎉 Party near Gion.", matching: "gion"))
        #expect(emoji.marks == [[14, 18]] && (emoji.text as NSString).substring(with: NSRange(location: 14, length: 4)) == "Gion")
        // Words that run over a sentence's end keep both sentences.
        let across = try #require(TodoSearch.sentence(in: "One. Two three. Four.", matching: "Two three. Four"))
        #expect(across.text == "Two three. Four.")
        #expect(TodoSearch.hits(["a": item, "b": TodoItem(id: "b", title: "Taxes")], words: "gion").keys.sorted() == ["a"])
        #expect(TodoSearch.hits(["a": item], words: "").isEmpty)
    }

    /// A result found by its notes says the sentence in place of its line 2, keeps its mark and says it to VoiceOver in
    /// the line's place; one found by its title keeps its line; no result offers Mark Done, which the same row offers
    /// outside the search. What was found is the row's own (`TodosListModel.Row.search`): its words stay as worked out.
    @MainActor @Test func aResultSaysWhatMatchedAndOffersNoInlineAction() throws {
        let item = TodoItem(id: "a", title: "Book the dentist", notes: "Ask for the 9:00 slot. Bring the card.")
        let view = TodoSummary(phase: TodoPhase.ready, section: TodoListSection.justFinished, reason: TodoReason.finishedSeen,
                               line: TodoLine(key: TodoLineKey.finishedSeen.rawValue), rank: 0)
        let c = TodoRowContentLogic.content(filed("a", TodoListSection.justFinished), item: item, view: view, now: now, words: words)
        #expect(c.offersMarkDone(nil) && c.found(by: nil) == nil && c.spoken(nil) == c.spoken, "outside the search")
        let byTitle = try #require(TodoSearch.hit(item, words: "dentist"))
        #expect(!c.offersMarkDone(byTitle) && c.found(by: byTitle) == nil, "found by its name: its line kept, no Mark Done")
        let byNotes = try #require(TodoSearch.hit(item, words: "card"))
        let found = try #require(c.found(by: byNotes))
        #expect(!c.offersMarkDone(byNotes) && found.text == "Bring the card.")
        let spoken = c.spoken(byNotes)
        #expect(spoken.hasPrefix("Book the dentist" + words.listSeparator) && spoken.contains("Bring the card.")
                && spoken == [c.spokenAround.head, "Bring the card.", c.spokenAround.tail].filter { !$0.isEmpty }
                    .joined(separator: words.listSeparator), "the sentence in the line's place: \(spoken)")
        // This device's own word on a write keeps line 2 ("Saving…"): no sentence over it.
        var saving = c
        saving.unsaved = true
        #expect(saving.found(by: byNotes) == nil)
        // The words on the accent's soft ground, in the primary color; the rest secondary.
        let marked = TodoFoundText.marked(TodoFoundLine(text: "Bring the card.", marks: [[10, 14]]), lead: 32)
        let lit = marked.runs.filter { $0.backgroundColor != nil }.map { String(marked[$0.range].characters) }
        #expect(lit == ["card"])
        #expect(marked.runs.filter { $0.backgroundColor == nil }.allSatisfy { $0.foregroundColor == .secondary })
        // A sentence whose words sit within about a line is shown whole; further in, it is cut a line's worth before them.
        let line = "Photo booth at the post office."
        #expect(String(TodoFoundText.marked(TodoFoundLine(text: line, marks: [[24, 30]]), lead: 32).characters) == line)
        let further = "Two of us, flying in on the Tuesday, staying four nights, a ryokan near Gion."
        let cut = String(TodoFoundText.marked(TodoFoundLine(text: further, marks: [[72, 76]]), lead: 32).characters)
        #expect(cut.hasPrefix("…") && cut.count == 1 + 32 + 5 && cut.hasSuffix(" a ryokan near Gion."), "\(cut)")
        #expect(TodoFoundText.lead(for: .large) == 32 && TodoFoundText.lead(for: .xxLarge) == 20
                && TodoFoundText.lead(for: .accessibility5) == 10)
    }

    /// A result offers no answers, as it offers no Mark Done: the row draws no answers' line, no card and no Undo, and
    /// VoiceOver's actions take them from the same `answers(_:)`. What this device did with the ask is a fact, not an
    /// answer: one found by its title still says it in line 2's place ("You chose …", "Not sure your answer went
    /// through"), never the hub's line that may still ask; one found by its notes says the sentence there, as any row.
    /// The row's words are worked out once: only `row.search` tells them apart.
    @MainActor @Test func aResultOffersNoAnswers() throws {
        let ask = OwnerAsk(id: "a", kind: OwnerAskKind.decision, home: OwnerAskHome.todo, round: "r", recommend: "Ryokan Hanami",
                           options: [.init(label: "Ryokan Hanami"), .init(label: "Kaede")], risk: OwnerAskRisk.safe)
        let view = TodoSummary(phase: TodoPhase.needsYou, section: TodoListSection.needsYou, reason: TodoReason.ownerDecides,
                               line: TodoLine(key: TodoLineKey.decision.rawValue, args: ["recommend": "Ryokan Hanami"]), asks: [ask])
        let item = TodoItem(id: "a", title: "Book a room in Kyoto", notes: "Two of us. A ryokan near Gion.")
        let row = filed("a", TodoListSection.needsYou)
        let open = TodoRowContentLogic.content(row, item: item, view: view, now: now, words: words)
        let answers = try #require(open.answers, "OK and Options outside the search")
        #expect(answers.primary == .ok(option: "Ryokan Hanami") && answers.options)
        #expect(open.answers(nil) == answers && open.spoken(nil) == open.spoken)
        let byTitle = try #require(TodoSearch.hit(item, words: "kyoto"))
        let byNotes = try #require(TodoSearch.hit(item, words: "gion"))
        #expect(open.answers(byTitle) == nil && open.answers(byNotes) == nil, "a result: no OK, no Options, no card")
        #expect(open.spoken(byTitle) == open.spoken, "found by its name: said as outside the search, its line kept")

        // An answer given here: line 2 says it, with Undo, outside the search; a result found by its title still says
        // it (`answers?.mark`, which the row's line 2 reads), without Undo or any other answer.
        let chosen = "You chose Kaede"
        let answered = TodoRowContentLogic.content(row, item: item, view: view, now: now, words: words,
                                                   answered: .chosen(chosen, undo: true))
        #expect(answered.answers(nil)?.mark == .chosen(chosen, undo: true) && answered.spoken(nil).contains(chosen))
        #expect(answered.answers(byTitle) == nil && answered.answers(byNotes) == nil, "no answer, no Undo on a result")
        #expect(answered.answers?.mark == .chosen(chosen, undo: true), "line 2's answer, kept for a result's line")
        #expect(answered.spoken(byTitle) == answered.spoken && answered.spoken(byTitle).contains(chosen)
                && answered.spoken(byTitle) != open.spoken, "not the hub's line in its place: \(answered.spoken(byTitle))")
        // Not sure it went through: a result says so too, as the list does, rather than the line that still asks.
        let unsure = TodoRowContentLogic.content(row, item: item, view: view, now: now, words: words, answered: .unsure)
        #expect(unsure.answers(byTitle) == nil && unsure.spoken(byTitle) == unsure.spoken
                && unsure.spoken(byTitle).contains("Not sure your answer went through"), "\(unsure.spoken(byTitle))")
        let sentence = try #require(answered.found(by: byNotes))
        #expect(answered.spoken(byNotes) == [answered.spokenAround.head, sentence.text, answered.spokenAround.tail]
                    .filter { !$0.isEmpty }.joined(separator: words.listSeparator) && !answered.spoken(byNotes).contains(chosen))
    }

    /// 2,000 to-dos with notes looked through, as the list does off the main actor at each key, and the list laid out
    /// from what was found: timed for the record (a Debug build on the simulator, so an upper bound), and bounded far
    /// above so a busy machine never fails it.
    @Test func twoThousandToDosWithNotes() async {
        let topics = ["ryokan", "passport", "dentist", "newsletter", "garden gate", "insurance", "photos", "widget"]
        var items: [String: TodoItem] = [:]
        var rows: [TodoFiled] = []
        for n in 0..<2_000 {
            let id = "t\(n)", topic = topics[n % topics.count]
            let notes = "Notes for \(topic) number \(n). Call them before Friday and keep the receipt. "
                + "Ask about the price, the dates and what is included; write it down here."
            items[id] = TodoItem(id: id, title: "To-do \(n) about \(topic)", notes: notes,
                                 doneAt: n % 5 == 0 ? now.addingTimeInterval(Double(-n * 600)) : nil)
            let section = n % 5 == 0 ? TodoListSection.done : [TodoListSection.todo, TodoListSection.working, TodoListSection.waiting][n % 3]
            rows.append(TodoFiled(id: id, manager: nil, section: section, closed: section == TodoListSection.done, rank: n,
                                  inactive: false, byView: true, quiet: false, partOf: nil, project: nil, asks: []))
        }
        let clock = ContinuousClock()
        var found: [String: TodoSearchHit] = [:]
        var times: [Duration] = []
        for query in ["receipt", "ryokan", "nothing like it"] { // every row's notes, an eighth's titles, none
            times.append(clock.measure { found = TodoSearch.hits(items, words: query) })
        }
        let start = clock.now
        let off = await TodoSearch.find(items, words: "receipt") // as the list runs it, off the main actor
        let offMain = clock.now - start
        #expect(off.count == 2_000)
        #expect(found.isEmpty)
        let hits = TodoSearch.hits(items, words: "receipt")
        #expect(hits.count == 2_000 && hits.values.allSatisfy { $0.found?.text.contains("receipt") == true })
        var input = TodosListLayout.Input(rows: rows, items: items, now: now, calendar: Calendar(identifier: .gregorian))
        input.query = "receipt"
        input.hits = hits
        var laid = TodosListLayout.Output.empty
        let layout = clock.measure { laid = TodosListLayout.layout(input) }
        #expect(laid.groups.flatMap(\.keys).count == 2_000)
        let ms = { (d: Duration) in String(format: "%.1f", Double(d.components.attoseconds) / 1e15 + Double(d.components.seconds) * 1000) }
        print("TODO-SEARCH-COST: 2000 to-dos with notes: look through (all match notes) \(ms(times[0])) ms, (titles) \(ms(times[1])) ms, "
              + "(none) \(ms(times[2])) ms; off the main actor (all match notes) \(ms(offMain)) ms; lay out 2000 results \(ms(layout)) ms")
        #expect(times.allSatisfy { $0 < .seconds(5) } && offMain < .seconds(5) && layout < .seconds(5))
    }
}

/// The list's model while it searches, against a `FakeHub` through a store of its own.
@MainActor
struct TodoSearchModelTests {
    private let now = Date(timeIntervalSince1970: 1_800_000_000)

    private func model(_ store: TodoStore, hub: FakeHub) -> TodosListModel {
        let now = now
        return TodosListModel(deps: TodosListDeps(store: store, outbox: TodosListModelTests.outbox(store, hub: hub), api: { hub.client },
                                                  now: { now }, calendar: { Calendar(identifier: .gregorian) }, projects: { [] },
                                                  toast: { _ in }, show: { _ in }, undoWindow: .seconds(60)))
    }

    private static let rows = [
        TodoViewJSON.row("n", title: "Pick the ryokan", view: TodoViewJSON.view(TodoListSection.needsYou, asks: ["a1"], line: "decision")),
        TodoViewJSON.row("n2", title: "Approve the plan", view: TodoViewJSON.view(TodoListSection.needsYou, rank: 1, asks: ["a2"], line: "decision")),
        TodoViewJSON.row("w", title: "Draft the newsletter", extra: #""notes":"Mention the ryokan trip. Keep it short.""#,
                         view: TodoViewJSON.view(TodoListSection.working)),
        TodoViewJSON.row("d", title: "Book the ryokan", extra: #""done_at":"2027-01-13T09:00:00Z""#,
                         view: TodoViewJSON.view(TodoListSection.done, phase: TodoPhase.done, line: "done")),
        TodoViewJSON.row("t", title: "Renew passport", view: TodoViewJSON.view(TodoListSection.todo, line: "rest")),
    ]

    private static func list(_ rows: [String] = rows) -> String {
        TodoViewJSON.list(rows, counts: TodoViewJSON.counts(needsYou: 2, inProgress: 1, open: 4,
            sections: [TodoListSection.needsYou: 2, TodoListSection.working: 1, TodoListSection.todo: 1, TodoListSection.done: 1]))
    }

    private static let places = #"{"places":[{"id":"all","kind":"all","title":"","needs_you":2}],"counts":{}}"#

    private func started(_ hub: FakeHub) async throws -> (TodoStore, TodosListModel) {
        hub.on("GET", "/api/v1/todos/places", json: Self.places)
        let store = TodoStore(cachedTodos: [], cacheWriter: nil, hubListChanged: {}, v1: .on())
        let list = model(store, hub: hub)
        list.start()
        await store.refresh(api: hub.client)
        try await waitForSearchTest { list.ready && list.hasRows }
        return (store, list)
    }

    /// Open and closed matches in the list's sections, Needs you first and Done last; each heading counts what matches
    /// (Needs you's too, in a plain number), while N, the drawer's number, does not change; a notes match says its
    /// sentence; Done today is not folded away.
    @Test func closedToDosAreFoundAndTheHeadingsCountMatches() async throws {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/todos", json: Self.list())
        let (store, list) = try await started(hub)
        defer { store.forget() }
        let n = list.needsYou
        #expect(n == 2 && !list.groups.flatMap(\.drawnKeys).contains("/d"), "the closed one is not in the list")
        list.query = "ryokan"
        try await waitForSearchTest { list.searchedWords == "ryokan" }
        #expect(list.groups.map(\.id) == [TodoListSection.needsYou.rawValue, TodoListSection.working.rawValue, "done:week"],
                "closed two days ago")
        #expect(list.groups.flatMap(\.drawnKeys) == ["/n", "/w", "/d"])
        #expect(list.groups.map(\.count) == [1, 1, 1], "what matches")
        #expect(list.needsYou == n && store.countsSource.needsYou == n, "N stays")
        #expect(list.row("/w")?.search?.found?.text == "Mention the ryokan trip.", "the notes' sentence")
        #expect(list.row("/n")?.search == TodoSearchHit(), "found by its name")
        list.query = ""
        #expect(!list.searching && list.groups.map(\.id).first == TodoListSection.needsYou.rawValue && list.groups[0].keys == ["/n", "/n2"],
                "cleared: the list again at once")
        #expect(list.row("/w")?.search == nil && list.row("/n")?.search == nil, "their own lines again")
    }

    /// Keys typed in a burst: each takes over from the look before it, so the list lands once, for the last words; what
    /// it showed stays until then, nothing being looked through on the main actor.
    @Test func keysInABurstLandOnceForTheLastWords() async throws {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/todos", json: Self.list())
        let (store, list) = try await started(hub)
        defer { store.forget() }
        let landings = list.searchCosts.landings
        for typed in ["p", "pa", "pas", "pass"] { list.query = typed }
        #expect(!list.searching && list.searchedWords.isEmpty, "nothing yet: the look runs off the main actor")
        try await waitForSearchTest { list.searching }
        #expect(list.searchedWords == "pass" && list.groups.flatMap(\.drawnKeys) == ["/t"])
        try await Task.sleep(for: .milliseconds(200)) // a look the burst left behind would land by now
        #expect(list.searchCosts.landings == landings + 1, "one landing for the burst")
        list.query = "pass " // a space after the words: the same search
        #expect(list.searchedWords == "pass" && list.searchCosts.landings == landings + 1)
    }

    /// A push while the list searches looks through the to-dos it changed: one whose notes now hold the words joins the
    /// results, one that no longer holds them leaves.
    @Test func aPushWhileSearchingLooksThroughWhatChanged() async throws {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/todos", json: Self.list())
        let (store, list) = try await started(hub)
        defer { store.forget() }
        list.query = "passport"
        try await waitForSearchTest { list.searchedWords == "passport" }
        #expect(list.groups.flatMap(\.drawnKeys) == ["/t"])
        var rows = Self.rows
        rows[1] = TodoViewJSON.row("n2", title: "Approve the plan", extra: #""notes":"Check the passport dates first.""#,
                                   view: TodoViewJSON.view(TodoListSection.needsYou, rank: 1, asks: ["a2"], line: "decision"))
        rows[4] = TodoViewJSON.row("t", title: "Renew the ID card", view: TodoViewJSON.view(TodoListSection.todo, line: "rest"))
        hub.on("GET", "/api/v1/todos", json: Self.list(rows))
        await store.refresh(api: hub.client)
        try await waitForSearchTest { list.groups.flatMap(\.drawnKeys) == ["/n2"] }
        #expect(list.row("/n2")?.search?.found?.text == "Check the passport dates first.")
    }

    /// A push of more to-dos than the main actor looks through is looked through off it again; a space typed after the
    /// words meanwhile is the same search, so that look goes on and lands (cancelled there, the results stayed behind
    /// the store until the next push). The space is typed on every turn of the main actor, so it meets the look.
    @Test func aSpaceTypedWhileAPushIsLookedThroughLetsItLand() async throws {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/todos", json: Self.list())
        let (store, list) = try await started(hub)
        defer { store.forget() }
        list.query = "passport"
        try await waitForSearchTest { list.searchedWords == "passport" }
        let more = (0..<80).map { n in
            TodoViewJSON.row("x\(n)", title: n == 79 ? "Passport photos" : "Errand \(n)", view: TodoViewJSON.view(TodoListSection.todo, line: "rest"))
        }
        hub.on("GET", "/api/v1/todos", json: Self.list(Self.rows + more))
        await store.refresh(api: hub.client)
        let found = { list.groups.flatMap(\.drawnKeys).contains("/x79") }
        let end = ContinuousClock.now + .seconds(5)
        var space = true
        while !found(), ContinuousClock.now < end {
            list.query = space ? "passport " : "passport"
            space.toggle()
            await Task.yield()
        }
        #expect(found() && list.searchedWords == "passport", "the push's match, in spite of the spaces")
        #expect(Set(list.groups.flatMap(\.drawnKeys)) == ["/t", "/x79"])
    }

    /// What the search costs the main actor over 2,000 to-dos with notes, as the owner meets it (P4, P12), beside what
    /// the list costs without it (a sort chosen, a push of one changed to-do): a look's landing when every to-do matches
    /// by its notes, another as the words grow (every sentence's marks move), a push of one changed to-do while
    /// searching, and the cleared field. What was found is each row's own, so none of them works a row's words out
    /// again. Timed for the record (a Debug build on the simulator, so an upper bound) and bounded far above so a busy
    /// machine never fails it.
    @Test func theSearchsMainActorCostOverTwoThousandToDos() async throws {
        let topics = ["ryokan", "passport", "dentist", "newsletter", "garden gate", "insurance", "photos", "widget"]
        let sections = [TodoListSection.todo, TodoListSection.working, TodoListSection.waiting]
        let rows = { (renamed: Set<Int>) in
            (0..<2_000).map { n in
                let topic = topics[n % topics.count], closed = n % 5 == 0
                let notes = "Notes for \(topic) number \(n). Call them before Friday and keep the receipt. "
                    + "Ask about the price, the dates and what is included; write it down here."
                let done = closed ? #","done_at":"2027-01-1\#(n % 5)T09:00:00Z""# : ""
                return TodoViewJSON.row("t\(n)", title: renamed.contains(n) ? "To-do \(n), renamed" : "To-do \(n) about \(topic)",
                                        extra: #""notes":"\#(notes)""# + done,
                                        view: TodoViewJSON.view(closed ? TodoListSection.done : sections[n % 3], rank: n,
                                                                phase: closed ? TodoPhase.done : nil, line: closed ? "done" : "rest"))
            }
        }
        let hub = FakeHub()
        hub.on("GET", "/api/v1/todos/places", json: Self.places)
        hub.on("GET", "/api/v1/todos", json: TodoViewJSON.list(rows([]), counts: TodoViewJSON.counts(needsYou: 0, open: 1_600)))
        let store = TodoStore(cachedTodos: [], cacheWriter: nil, hubListChanged: {}, v1: .on())
        defer { store.forget() }
        let list = model(store, hub: hub)
        list.start()
        await store.refresh(api: hub.client)
        try await waitForSearchTest(seconds: 60) { list.ready && list.hasRows }
        // The list's own costs, the field empty: a sort chosen, and a push of one changed to-do.
        list.sort = .activity
        let sorted = list.searchCosts.relayout
        list.sort = .suggested
        hub.on("GET", "/api/v1/todos", json: TodoViewJSON.list(rows([2]), counts: TodoViewJSON.counts(needsYou: 0, open: 1_600)))
        await store.refresh(api: hub.client)
        try await waitForSearchTest(seconds: 60) { list.row("/t2")?.content.title == "To-do 2, renamed" }
        let plainPush = list.searchCosts.push
        list.query = "receipt"
        try await waitForSearchTest(seconds: 60) { list.searchedWords == "receipt" }
        let first = list.searchCosts.landing
        #expect(list.groups.flatMap(\.drawnKeys).count == 2_000 && list.row("/t3")?.search?.found != nil)
        list.query = "the receipt"
        try await waitForSearchTest(seconds: 60) { list.searchedWords == "the receipt" }
        let grown = list.searchCosts.landing
        hub.on("GET", "/api/v1/todos", json: TodoViewJSON.list(rows([1, 2]), counts: TodoViewJSON.counts(needsYou: 0, open: 1_600)))
        await store.refresh(api: hub.client)
        try await waitForSearchTest(seconds: 60) { list.row("/t1")?.content.title == "To-do 1, renamed" }
        let push = list.searchCosts.push
        list.query = ""
        let cleared = list.searchCosts.cleared
        #expect(!list.searching && list.row("/t3")?.search == nil)
        let ms = { (d: Duration) in String(format: "%.1f", Double(d.components.attoseconds) / 1e15 + Double(d.components.seconds) * 1000) }
        print("TODO-SEARCH-COST: model, 2000 to-dos with notes, main actor: without a search, a sort chosen \(ms(sorted)) ms, "
              + "a push of one change \(ms(plainPush)) ms; searching, landing (all match) \(ms(first)) ms, "
              + "landing (the marks move) \(ms(grown)) ms, a push of one change \(ms(push)) ms, cleared \(ms(cleared)) ms")
        #expect([sorted, plainPush, first, grown, push, cleared].allSatisfy { $0 < .seconds(5) })
    }
}

/// Waits (up to `seconds`, 5 by default) for something the search or the store does.
@MainActor
private func waitForSearchTest(seconds: Int = 5, _ condition: @MainActor () -> Bool) async throws {
    for _ in 0..<(seconds * 50) where !condition() { try await Task.sleep(for: .milliseconds(20)) }
    try #require(condition(), "it did not happen in time")
}
