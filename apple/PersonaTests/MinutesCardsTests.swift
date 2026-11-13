import Foundation
import Testing
@testable import Persona

/// A recording's Summary on iPhone (#5026, docs/apple/recording.md「Minutes」): which view the page shows, which cards the
/// Summary has and in what order, what an item carries, and the transcript lines it opens to (`RecordingMinutesCards`).
struct MinutesCardsTests {
    /// The made-up meeting's transcript (`RecordingSamples.pastMeeting`'s times).
    private let lines = [
        RecordingSentence(start: 2, end: 14, text: "intro"), RecordingSentence(start: 21, end: 38, text: "lock screen"),
        RecordingSentence(start: 44, end: 58, text: "decided"), RecordingSentence(start: 63, end: 80, text: "owner"),
        RecordingSentence(start: 86, end: 101, text: "not settled"), RecordingSentence(start: 105, end: 118, text: "next week"),
    ]

    private var minutes: RecordingMinutes {
        RecordingMinutes(title: "Weekly", when: "2027-01-15 16:00–17:00", place: "Room 3", attendees: ["Ann", "Bo", "Cy"],
                         points: [.init(text: "intro", at: 2), .init(text: "lock screen", at: 21)],
                         decisions: [.init(text: "decided", at: 44)],
                         actions: [.init(text: "owner", at: 63, who: "Ann", by: "Friday")],
                         openQuestions: [.init(text: "not settled", at: 86)])
    }

    // MARK: Which view

    /// No switch before the minutes are ready; then the Summary, unless the page was switched.
    @Test func summaryIsTheDefaultOnceTheMinutesAreReady() {
        #expect(RecordingMinutesCards.pane(picked: nil, ready: false) == nil)
        #expect(RecordingMinutesCards.pane(picked: .transcript, ready: false) == nil)
        #expect(RecordingMinutesCards.pane(picked: nil, ready: true) == .summary)
        #expect(RecordingMinutesCards.pane(picked: .transcript, ready: true) == .transcript)
        #expect(RecordingMinutesCards.pane(picked: .summary, ready: true) == .summary)
    }

    // MARK: Which cards

    @Test func cardsComeInTheSummarysOrder() {
        let summary = RecordingMinutesCards.summary(minutes, sentences: lines)
        #expect(summary.cards.map(\.kind) == [.points, .decisions, .open, .actions])
        #expect(summary.cards.map(\.items.count) == [2, 1, 1, 1])
        #expect(summary.cards.map(\.kind.symbol) == [Theme.Icon.keyPoint, Theme.Icon.decision, Theme.Icon.openQuestion, Theme.Icon.todo])
    }

    /// A group with nothing in it has no card; an item with no words is not a row.
    @Test func emptyGroupsAndBlankItemsAreLeftOut() {
        var some = minutes
        some.decisions = []
        some.openQuestions = [.init(text: "  \n", at: 90)]
        let summary = RecordingMinutesCards.summary(some, sentences: lines)
        #expect(summary.cards.map(\.kind) == [.points, .actions])
        #expect(RecordingMinutesCards.summary(RecordingMinutes(), sentences: lines).cards.isEmpty)
    }

    /// An item keeps its place in the hub's list as its id (a blank one before it still counts), its time, and for an
    /// action who and by when.
    @Test func itemsCarryTheirTimeAndByline() throws {
        var some = minutes
        some.points.insert(.init(text: ""), at: 0)
        let summary = RecordingMinutesCards.summary(some, sentences: lines)
        let points = try #require(summary.cards.first { $0.kind == .points })
        #expect(points.items.map(\.id) == ["points-1", "points-2"])
        let action = try #require(summary.cards.first { $0.kind == .actions }?.items.first)
        #expect(action.text == "owner")
        #expect(action.at == 63)
        #expect(action.byline == "Ann · Friday")
        #expect(points.items.first?.byline == nil)
    }

    /// The header's lines: the minutes' own title, when · where, and who was there; nothing for what they leave out.
    @Test func headerLines() {
        let summary = RecordingMinutesCards.summary(minutes, sentences: lines)
        #expect(summary.title == "Weekly")
        #expect(summary.facts == "2027-01-15 16:00–17:00 · Room 3")
        #expect(["Ann", "Bo", "Cy"].allSatisfy { summary.attendees?.contains($0) == true })
        let bare = RecordingMinutesCards.summary(RecordingMinutes(title: "", place: "Room 3", points: [.init(text: "a")]), sentences: [])
        #expect(bare.title == nil)
        #expect(bare.facts == "Room 3")
        #expect(bare.attendees == nil)
    }

    /// When is the app's own date style from `start` / `end` (#4875), as the minutes' sections write it; the hub's `when`
    /// only without them.
    @Test func headerWhenIsTheAppsDateStyle() throws {
        var dated = minutes
        dated.start = try #require(JSON.parseDate("2027-01-15T16:00:00+08:00"))
        dated.end = try #require(JSON.parseDate("2027-01-15T17:00:00+08:00"))
        let summary = RecordingMinutesCards.summary(dated, sentences: lines)
        let when = try #require(dated.whenLine)
        #expect(summary.facts == when + " · Room 3")
        #expect(summary.facts?.contains("2027-01-15 16:00") == false)
    }

    /// An item that cites a meeting photo (#3862 phase 3) carries its number, for the card's thumbnail.
    @Test func itemsCarryTheirCitedPhoto() throws {
        var some = minutes
        some.points.append(.init(text: "the whiteboard", at: 30, photo: 3))
        let summary = RecordingMinutesCards.summary(some, sentences: lines)
        let points = try #require(summary.cards.first { $0.kind == .points })
        #expect(points.items.map(\.photo) == [nil, nil, 3])
    }

    /// Action items as to-dos (#4062): the hub's name of each and whether it is one; Add All sends those left, only for
    /// numbered minutes, and never one without a name.
    @Test func actionItemsCarryWhatAddToToDosSends() throws {
        var some = minutes
        some.version = 2
        some.actions = [.init(text: "one", at: 63, id: "a1"), .init(text: "two", at: 70, id: "a2", todoID: "t9"),
                        .init(text: "three", at: 75, id: "a3"), .init(text: "four", at: 80, id: "")]
        let summary = RecordingMinutesCards.summary(some, sentences: lines)
        let actions = try #require(summary.cards.first { $0.kind == .actions })
        #expect(actions.items.map(\.action) == ["a1", "a2", "a3", nil])
        #expect(actions.items.map(\.added) == [false, true, false, false])
        #expect(summary.offered)
        #expect(summary.addable == ["a1", "a3"])
        // A point's id is not an action's.
        some.points = [.init(text: "p", at: 2, id: "p1")]
        #expect(RecordingMinutesCards.summary(some, sentences: lines).cards.first?.items.first?.action == nil)
        // Minutes the hub doesn't number offer none.
        some.version = nil
        let old = RecordingMinutesCards.summary(some, sentences: lines)
        #expect(!old.offered)
        #expect(old.addable.isEmpty)
    }

    // MARK: The lines an item opens to

    /// The line its time picks, one before it and two after it (`Theme.Recording.contextBefore` / `contextAfter`).
    @Test func contextIsTheLineAndItsNeighbours() throws {
        let summary = RecordingMinutesCards.summary(minutes, sentences: lines)
        let decision = try #require(summary.cards.first { $0.kind == .decisions }?.items.first)
        #expect(decision.context.map(\.id) == [1, 2, 3, 4])
        #expect(decision.context.map(\.text) == ["lock screen", "decided", "owner", "not settled"])
        #expect(decision.context.filter(\.said).map(\.id) == [2])
        #expect(decision.context.first?.start == 21)
    }

    /// At the transcript's ends it keeps what there is; a time between two lines picks the one said by then.
    @Test func contextStaysInsideTheTranscript() {
        #expect(RecordingMinutesCards.context(at: 2, in: lines, before: 1, after: 2).map(\.id) == [0, 1, 2])
        #expect(RecordingMinutesCards.context(at: 110, in: lines, before: 1, after: 2).map(\.id) == [4, 5])
        #expect(RecordingMinutesCards.context(at: 50, in: lines, before: 1, after: 2).filter(\.said).map(\.id) == [2])
        #expect(RecordingMinutesCards.context(at: 44, in: lines, before: 0, after: 0).map(\.id) == [2])
    }

    /// No time, or no transcript: nothing to open.
    @Test func contextNeedsATimeAndATranscript() {
        #expect(RecordingMinutesCards.context(at: nil, in: lines, before: 1, after: 2).isEmpty)
        #expect(RecordingMinutesCards.context(at: 44, in: [], before: 1, after: 2).isEmpty)
        #expect(RecordingMinutesCards.context(at: .nan, in: lines, before: 1, after: 2).isEmpty)
        var untimed = minutes
        untimed.points = [.init(text: "said sometime")]
        let summary = RecordingMinutesCards.summary(untimed, sentences: lines)
        #expect(summary.cards.first?.items.first?.context.isEmpty == true)
    }
}
