import Foundation
import Observation
import Testing
@testable import Persona
import PersonaCoreTestSupport
import PersonaWire

/// Rows read from Unread or Just finished keep their place until the list is refreshed (#12514): what the model holds
/// for a visit (`AppModel.seenThisVisit`), what the list's projection derives from it, and the shared cases in
/// spec/fixtures/sort/next.
@MainActor struct SeenThisVisitTests {
    private let machine = MachineRef(group: "", id: "m")

    private func session(_ id: String, state: String, updated: TimeInterval = 100, justFinished: Bool = false) -> Session {
        let f = ISO8601DateFormatter()
        let work = justFinished ? #","work":{"phase":"done","just_finished":true}"# : ""
        let json = """
        {"machine_id":"m","session_id":"\(id)","title":"\(id)","cwd":"/tmp/p","status":"done","state":"\(state)",
         "space_id":"w1","space_name":"app","updated_at":"\(f.string(from: Date(timeIntervalSince1970: updated)))"\(work)}
        """
        do { return try JSON.decoder.decode(Session.self, from: Data(json.utf8)) }
        catch { fatalError("bad fixture: \(error)") }
    }

    private func key(_ id: String) -> SessionKey { SessionKey(machineID: "m", sessionID: id) }

    private func makeModel(_ mode: SeenRows, _ sessions: [Session]) -> (AppModel, URL) {
        let folder = FileManager.default.temporaryDirectory.appending(path: UUID().uuidString)
        let model = AppModel(stateFolder: folder)
        model.seenRowsForTests = mode
        model.applySessions([machine: sessions])
        return (model, folder)
    }

    // MARK: The model

    /// Opened after the list showed it, marked from its menu, or seen elsewhere after the list showed it: each keeps the
    /// place it was shown in; leaving the chat does not let it go.
    @Test func readRowsAreHeldUntilTheListIsRefreshed() {
        let (model, folder) = makeModel(.stay, [session("open", state: "unread"), session("menu", state: "unread"),
                                            session("done", state: "idle", justFinished: true), session("away", state: "unread")])
        defer { try? FileManager.default.removeItem(at: folder) }
        // The list shows "open" and "away" unread.
        model.noteShownUnseen([key("open"): .unread, key("away"): .unread])
        model.visibleSession = key("open")
        model.markSeen(key("open"))
        model.visibleSession = nil
        #expect(model.heldInList == nil, "the open one is let go of as it was")
        model.markSeen(key("menu"))
        model.markSeen(key("done"))
        #expect(model.seenThisVisit == [key("open"): .unread, key("menu"): .unread, key("done"): .justFinished])
        #expect(model.shownThisVisit == [key("away"): .unread], "still unseen: noted, not held")
        // "away" was seen on another device: the list holds it by what it showed, from the push that says so.
        model.applySessions([machine: [session("open", state: "idle"), session("menu", state: "idle"),
                                       session("done", state: "idle"), session("away", state: "idle")]])
        #expect(model.seenThisVisit == [key("open"): .unread, key("away"): .unread, key("menu"): .unread, key("done"): .justFinished])
        #expect(model.shownThisVisit.isEmpty)
        model.releaseSeen([key("menu")])
        #expect(model.seenThisVisit[key("menu")] == nil && model.seenThisVisit.count == 3, "a section's Clear Seen")
        model.releaseAllSeen()
        #expect(model.seenThisVisit.isEmpty, "a pull to refresh")
    }

    /// Noting the rows the list shows unseen changes nothing the list watches: only a row seen since does (the list's
    /// projection is keyed by `seenThisVisit`, so a row that finishes derives it once, not twice).
    @Test func notingUnseenRowsChangesNothingTheListWatches() {
        let (model, folder) = makeModel(.stay, [session("a", state: "unread"), session("b", state: "unread")])
        defer { try? FileManager.default.removeItem(at: folder) }
        nonisolated(unsafe) var changed = false // the change handler fires once, synchronously, on the write
        withObservationTracking { _ = model.seenThisVisit } onChange: { changed = true }
        model.noteShownUnseen([key("a"): .unread, key("b"): .unread])
        #expect(!changed, "noted, not watched")
        #expect(model.shownThisVisit.count == 2 && model.seenThisVisit.isEmpty)
        model.markSeen(key("a"))
        #expect(changed, "a read row is")
        #expect(model.seenThisVisit == [key("a"): .unread] && model.shownThisVisit == [key("b"): .unread])
    }

    /// A row the list noted unseen and already seen by the time it is noted (the list was derived before the push that
    /// said so) is held at once; a held row the list shows unseen in another section again is held there once seen.
    @Test func aNotedRowSeenMeanwhileIsHeldAndAHeldRowShownAgainMoves() {
        let (model, folder) = makeModel(.stay, [session("late", state: "idle"), session("again", state: "unread")])
        defer { try? FileManager.default.removeItem(at: folder) }
        model.noteShownUnseen([key("late"): .unread])
        #expect(model.seenThisVisit == [key("late"): .unread] && model.shownThisVisit.isEmpty)
        model.markSeen(key("again"))
        #expect(model.seenThisVisit[key("again")] == .unread)
        // It worked and finished unseen again, now shown in Just finished.
        model.applySessions([machine: [session("late", state: "idle"), session("again", state: "idle", justFinished: true)]])
        model.noteShownUnseen([key("again"): .justFinished])
        #expect(model.seenThisVisit[key("again")] == .justFinished)
        model.markSeen(key("again"))
        #expect(model.seenThisVisit == [key("late"): .unread, key("again"): .justFinished] && model.shownThisVisit.isEmpty)
    }

    /// A chat that finished while it was open was never in Unread on the list: it is held while open only, as before.
    @Test func aChatThatFinishedWhileOpenIsNotHeldAfterIt() {
        let (model, folder) = makeModel(.stay, [session("chat", state: "unread")])
        defer { try? FileManager.default.removeItem(at: folder) }
        model.visibleSession = key("chat")
        model.markSeen(key("chat"))
        #expect(model.heldInList == key("chat") && model.seenThisVisit.isEmpty)
        model.visibleSession = nil
        #expect(model.heldInList == nil)
    }

    /// A refresh lets the read rows go and starts the next visit with the rows the list shows unseen now, so one of them
    /// read after the refresh is held again; with seen rows leaving with their chat, nothing is kept.
    @Test func aRefreshStartsTheNextVisitWithTheRowsStillUnseen() {
        let (model, folder) = makeModel(.stay, [session("read", state: "unread"), session("next", state: "unread")])
        defer { try? FileManager.default.removeItem(at: folder) }
        model.noteShownUnseen([key("read"): .unread, key("next"): .unread])
        model.markSeen(key("read"))
        model.releaseAllSeen(keeping: [key("next"): .unread])
        #expect(model.seenThisVisit.isEmpty, "the read row goes")
        #expect(model.shownThisVisit == [key("next"): .unread], "the unseen one starts the next visit")
        model.visibleSession = key("next")
        model.markSeen(key("next"))
        model.visibleSession = nil
        #expect(model.seenThisVisit == [key("next"): .unread], "opened after the refresh, it is held")
        model.seenRowsForTests = .leave
        model.releaseAllSeen(keeping: [key("next"): .unread])
        #expect(model.seenThisVisit.isEmpty && model.shownThisVisit.isEmpty)
    }

    /// Mark All as Seen is done with its rows: they leave at once, those already seen in place too.
    @Test func markAllAsSeenLetsItsRowsGo() {
        let (model, folder) = makeModel(.stay, [session("a", state: "unread"), session("b", state: "unread"), session("c", state: "unread")])
        defer { try? FileManager.default.removeItem(at: folder) }
        model.markSeen(key("a"))
        model.noteShownUnseen([key("b"): .unread, key("c"): .unread])
        model.markSeen([key("a"), key("b")])
        #expect(model.seenThisVisit.isEmpty, "the one read before goes with the batch")
        #expect(model.shownThisVisit == [key("c"): .unread], "and the batch's unread one is not held once seen")
        #expect(model.session(key("b"))?.needsSeen() == false)
    }

    /// A space's Mark All as Seen (its ⋯, and a repository's) passes all of its rows: a row read there before and held
    /// leaves with the unread one, as under the Just finished header.
    @Test func aSpacesMarkAllLetsItsHeldRowsGoWithItsUnreadOnes() throws {
        let (model, folder) = makeModel(.stay, [session("read", state: "unread"), session("unread", state: "unread")])
        defer { try? FileManager.default.removeItem(at: folder) }
        model.noteShownUnseen([key("read"): .unread, key("unread"): .unread])
        model.markSeen(key("read"))
        #expect(model.seenThisVisit == [key("read"): .unread])
        let space = try #require(SpaceGroup.build(model.allSessions).first)
        #expect(space.sessions.count == 2, "both in one space")
        model.markSeen(space.sessions.map(\.key))
        #expect(model.seenThisVisit.isEmpty && model.shownThisVisit.isEmpty, "both leave")
        #expect(model.session(key("unread"))?.needsSeen() == false)
    }

    /// The counts know only the hub's state: rows read and held in place leave them at once, as any read row does. The
    /// Home widget's finished rows and the Unread filter drop by the rows read; the tab's badge counts who needs the user,
    /// never these. The Live Activity's numbers are the hub's (`FleetState`), told by the same seen marks.
    @Test func readRowsLeaveTheCountsWhileTheyAreHeld() {
        let ids = ["a", "b", "c", "d", "e", "f"]
        let (model, folder) = makeModel(.stay, ids.map { session($0, state: "unread") })
        defer { try? FileManager.default.removeItem(at: folder) }
        model.noteShownUnseen(Dictionary(uniqueKeysWithValues: ids.map { (key($0), AgentListOrder.Held.unread) }))
        func finished() -> Int {
            let agents = HomeAgents.build(needsYou: [], sessions: model.allSessions, place: { _ in .agents }, session: { model.session($0) },
                                          machineName: { _ in nil }, isTask: { _ in false })
            return agents.rows.filter { $0.state == .done }.count + agents.more.done
        }
        let badges = model.badges
        #expect(finished() == 6)
        for id in ids.prefix(5) { model.markSeen(key(id)) }
        #expect(model.seenThisVisit.count == 5, "the five read rows are held in Unread")
        #expect(finished() == 1, "the widget's finished rows drop by the five read")
        var settings = AgentListProjection.Settings(grouping: "state")
        settings.heldSeen = model.seenThisVisit
        let projection = AgentListProjection.build(.init(sessions: model.allSessions, machines: [], settings: settings))
        #expect(projection.filterCounts[.unread] == 1, "the Unread filter counts what is still unread")
        #expect(projection.heldSeen.count == 5)
        #expect(model.badges == badges, "the tab's badge counts who needs you, never these")
    }

    /// With seen rows leaving with their chat, nothing is held past the open one, as before #12514.
    @Test func leavingWithTheChatHoldsOnlyTheOpenRow() {
        let (model, folder) = makeModel(.leave, [session("open", state: "unread"), session("other", state: "unread")])
        defer { try? FileManager.default.removeItem(at: folder) }
        model.visibleSession = key("open")
        model.markSeen(key("open"))
        model.noteShownUnseen([key("other"): .unread])
        #expect(model.heldInList == key("open") && model.seenThisVisit.isEmpty && model.shownThisVisit.isEmpty)
        model.visibleSession = nil
        #expect(model.heldInList == nil)
    }

    /// A forgotten pairing takes its held rows with it; another group's stay.
    @Test func forgettingAGroupLetsItsHeldRowsGo() {
        var gone = session("gone", state: "unread"), kept = session("kept", state: "unread")
        gone.group = UUID().uuidString
        kept.group = UUID().uuidString
        let folder = FileManager.default.temporaryDirectory.appending(path: UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: folder) }
        let model = AppModel(stateFolder: folder)
        model.seenRowsForTests = .stay
        model.applySessions([gone.machineRef: [gone], kept.machineRef: [kept]])
        model.noteShownUnseen([gone.key: .unread, kept.key: .unread])
        model.markSeen(gone.key)
        #expect(model.seenThisVisit == [gone.key: .unread])
        model.forgetGroups([gone.group])
        #expect(model.seenThisVisit.isEmpty && model.shownThisVisit == [kept.key: .unread])
    }

    // MARK: The projection

    /// The list says which rows it shows as unseen (what a visit notes) and which held ones are seen now (what the notice
    /// and Clear Seen count); counts are the hub's states, whatever is held.
    @Test func theProjectionNamesTheUnseenRowsAndTheHeldSeenOnes() {
        let read = session("read", state: "idle", updated: 50), open = session("open", state: "unread", updated: 40)
        let done = session("done", state: "idle", updated: 30, justFinished: true), old = session("old", state: "idle", updated: 20)
        var settings = AgentListProjection.Settings(grouping: "state")
        settings.heldSeen = [read.key: .unread, open.key: .unread]
        let projection = AgentListProjection.build(.init(sessions: [read, open, done, old], machines: [], settings: settings))
        #expect(projection.needsSeen == [open.key: .unread, done.key: .justFinished])
        #expect(projection.heldSeen == [read.key])
        #expect(projection.filterCounts[.unread] == 1, "the Unread filter counts only what is still unread")
        #expect(projection.unread == [open.key], "Mark All as Seen takes only what is still unread")
    }

    // MARK: The shared cases (spec/fixtures/sort/next)

    /// Each case's input as iOS's projection takes it (the shadow tests' converter, plus `held_seen`).
    private func nativeInput(_ fixture: ProjectionFixture) throws -> AgentListProjection.Input {
        let input = fixture.input
        let settings = try ProjectionFixture.require(input["settings"], "settings")
        var preferences = AgentListProjection.Preferences()
        preferences.pins = try JSON.decoder.decode(Pins.self, from: Data((input["pins"] ?? .object([:])).encoded().utf8))
        preferences.mutes = try JSON.decoder.decode(Pins.self, from: Data((input["mutes"] ?? .object([:])).encoded().utf8))
        let machines = try (input["machines"]?.array ?? []).map { machine -> Machine in
            var fields = try ProjectionFixture.require(machine.object, "machine")
            fields["kind"] = .string("local")
            fields["online"] = .bool(true)
            return try JSON.decoder.decode(Machine.self, from: Data(ContinuityJSON.object(fields).encoded().utf8))
        }
        var native = AgentListProjection.Settings(grouping: settings.text("grouping") ?? "state",
                                                  filter: AgentFilter(rawValue: settings.text("quick") ?? "all") ?? .all,
                                                  query: settings.text("query") ?? "", machine: settings.text("machine_filter") ?? "",
                                                  preferences: ["": preferences])
        if let held = settings.text("held") { native.held = SessionKey(path: held) }
        native.kept = Set((settings["kept"]?.array?.compactMap(\.string) ?? []).compactMap(SessionKey.init(path:)))
        for (path, place) in settings["held_seen"]?.object ?? [:] {
            let key = try ProjectionFixture.require(SessionKey(path: path), "held_seen key \(path)")
            native.heldSeen[key] = place.string == "just_finished" ? .justFinished : .unread
        }
        let sessions = try (input["sessions"]?.array ?? []).map { try JSON.decoder.decode(Session.self, from: Data($0.encoded().utf8)) }
        return .init(sessions: sessions, machines: machines, settings: native)
    }

    @Test func swiftReplaysTheVisitCases() throws {
        let fixtures = try ProjectionFixture.cases(in: "sort/next", operation: "agents_list")
        #expect(fixtures.count >= 5)
        for fixture in fixtures {
            let input = try nativeInput(fixture)
            let settings = fixture.input["settings"]
            var presentation = AgentSortPresentation()
            presentation.folded = Set(settings?["folded"]?.array?.compactMap(\.string) ?? [])
            presentation.expandedIdle = Set(settings?["expanded_idle"]?.array?.compactMap(\.string) ?? [])
            presentation.expandedQuiet = Set(settings?["expanded_quiet"]?.array?.compactMap(\.string) ?? [])
            let projection = AgentListProjection.build(input)
            let native = AgentSortComparable(native: input, projection: projection, presentation: presentation)
            let order = input.settings.order
            let expected = fixture.expected
            let counts = try ProjectionFixture.require(expected["counts"]?.object, "\(fixture.name) counts")
            for (name, value) in counts {
                #expect(native.filterCounts[name] == value.int, "\(fixture.name): counts.\(name)")
            }
            #expect(native.unread == (expected["unread_shown"]?.array?.compactMap(\.string) ?? []), "\(fixture.name): unread_shown")
            let groups = expected["groups"]?.array ?? []
            #expect(native.groups.map(\.id) == groups.compactMap { $0.text("id") }, "\(fixture.name): groups")
            for (group, want) in zip(native.groups, groups) {
                #expect(group.rows == (want["rows"]?.array?.compactMap(\.string) ?? []), "\(fixture.name): \(group.id) rows")
                #expect(group.visibleRows == (want["visible_rows"]?.array?.compactMap(\.string) ?? []), "\(fixture.name): \(group.id) visible_rows")
                #expect(group.folded == want["folded"]?.bool, "\(fixture.name): \(group.id) folded")
                if let counts = want["state_counts"]?.object {
                    #expect(group.stateCounts == counts.mapValues { $0.int ?? -1 }, "\(fixture.name): \(group.id) state_counts")
                }
                // The rows drawn as Seen in the place they were held in: what the header leaves out and Clear Seen counts.
                let heldSeen = group.rows.filter { path in
                    SessionKey(path: path).flatMap { projection.sessionsByKey[$0] }.map(order.isHeldSeen) == true
                }
                #expect(heldSeen == (want["held_seen"]?.array?.compactMap(\.string) ?? []), "\(fixture.name): \(group.id) held_seen")
            }
        }
    }
}
