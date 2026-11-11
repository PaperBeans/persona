import Foundation
import Testing
@testable import Persona

/// Persona in a recorded meeting (#2981; contract docs/assistant/meeting-minutes.md「Attending a meeting」, the phone's side
/// docs/apple/recording.md「Persona in meetings」): what an upload carries, the live log's rules, sending it, asking by a
/// tap, when an answer is said out loud, and Persona's own voice left out of the transcript.
@MainActor struct MeetingAttendanceTests {
    private let agent = SessionKey(machineID: "mac", sessionID: "s1")

    private func meeting(_ role: MeetingAttendance, session: SessionKey? = nil, active: Bool = true,
                         lines: [RecordingSentence] = []) -> RecordingEntry {
        var e = RecordingEntry(id: UUID(), createdAt: Date(timeIntervalSince1970: 1_800_000_000), source: .phone, title: "Weekly",
                               locale: "zh-CN", active: active)
        e.meeting = RecordingMeeting(title: "Weekly")
        e.attendance = RecordingAttendance(role: role, session: session?.path, sessionTitle: "Persona")
        if !lines.isEmpty { e.segments = [RecordingSegment(index: 0, start: 0, duration: 60, sentences: lines)] }
        return e
    }

    private func json(_ e: RecordingEntry) throws -> [String: Any] {
        try JSONSerialization.jsonObject(with: JSON.encoder.encode(RecordingUpload(e))) as! [String: Any]
    }

    private func line(_ start: Double, _ end: Double, _ text: String = "line") -> RecordingSentence {
        RecordingSentence(start: start, end: end, text: text)
    }

    // MARK: The upload

    /// A meeting Persona never attended uploads exactly as before; one it attends says the role, and names the session
    /// only when it isn't the main chat.
    @Test func theUploadCarriesTheAttendance() throws {
        var e = meeting(.off)
        e.attendance = nil
        let before = try json(e)
        #expect(before["attendance"] == nil && before["attending_session"] == nil)

        e.attendance = RecordingAttendance(role: .participant)
        let main = try json(e)
        #expect(main["attendance"] as? String == "participant")
        #expect(main["attending_session"] == nil, "the main chat is the absent session")
        #expect(Set(main.keys).subtracting(before.keys) == ["attendance"])

        e.attendance = RecordingAttendance(role: .listener, session: agent.path)
        let named = try #require(try json(e)["attending_session"] as? [String: String])
        #expect(named == ["machine_id": "mac", "session_id": "s1"])

        e.attendance?.set(.off, at: 30)
        let off = try json(e)
        #expect(off["attendance"] as? String == "off")
        #expect(off["attending_session"] == nil)
    }

    /// Notes and watch recordings never carry a role, whatever they hold.
    @Test func notesAndWatchRecordingsNeverCarryARole() throws {
        var note = meeting(.listener)
        note.meeting = nil
        #expect(try json(note)["attendance"] == nil)
        var watch = meeting(.listener)
        watch.source = .watch
        #expect(try json(watch)["attendance"] == nil)
        #expect(!watch.attended)
    }

    /// The contract's first exception: an attending meeting goes before it has any text.
    @Test func anAttendingMeetingUploadsBeforeAnyText() {
        #expect(RecordingSync.uploadable(meeting(.listener)))
        var silent = meeting(.off)
        silent.attendance = nil
        #expect(!RecordingSync.uploadable(silent))
    }

    // MARK: The role

    @Test func onlyTakingPartSpeaks() {
        #expect(!MeetingAttendance.off.attends && !MeetingAttendance.off.speaks)
        #expect(MeetingAttendance.listener.attends && !MeetingAttendance.listener.speaks)
        #expect(MeetingAttendance.participant.attends && MeetingAttendance.participant.speaks)
    }

    /// Off until the hub offers attending; then the hub's default.
    @Test func theCachedDefaultFollowsTheHub() {
        let store = UserDefaults(suiteName: "meeting-attendance-\(UUID())")!
        #expect(MeetingAttendanceDefaults.cachedRole(store) == .off)
        MeetingAttendanceDefaults.remember(offered: true, role: .participant, store)
        #expect(MeetingAttendanceDefaults.cachedRole(store) == .participant)
        MeetingAttendanceDefaults.remember(offered: false, role: nil, store)
        #expect(MeetingAttendanceDefaults.cachedRole(store) == .off, "a hub that no longer offers it: off")
    }

    /// Asking is open only in a meeting being recorded on this phone, with a role, and a hub that took it.
    @Test func attendedNeedsARoleARecordingAndAHub() {
        #expect(meeting(.listener).attended)
        #expect(meeting(.participant, session: agent).attended)
        #expect(!meeting(.off).attended)
        #expect(!meeting(.listener, active: false).attended)
        var stopped = meeting(.listener)
        stopped.attendance?.stopped = .unsupported
        #expect(!stopped.attended, "an older manager took the upload without the role")
        var full = meeting(.listener)
        full.attendance?.stopped = .liveFull
        #expect(full.attended, "no room for more live lines; questions still work")
    }

    /// Kept with the recording; a copy from before attendance reads as none, a role from a later app as off.
    @Test func attendanceIsKeptWithTheRecording() throws {
        var e = meeting(.participant, session: agent)
        e.attendance?.live = [line(1, 2)]
        let back = try JSONDecoder().decode(RecordingEntry.self, from: JSONEncoder().encode(e))
        #expect(back.attendance == e.attendance)
        #expect(back.attendance?.key == agent)
        var old = try #require(try JSONSerialization.jsonObject(with: JSONEncoder().encode(e)) as? [String: Any])
        old.removeValue(forKey: "attendance")
        let older = try JSONDecoder().decode(RecordingEntry.self, from: JSONSerialization.data(withJSONObject: old))
        #expect(older.attendance == nil)
        let later = try JSONDecoder().decode(RecordingAttendance.self, from: Data(#"{"role":"chairing","session":"mac/s1"}"#.utf8))
        #expect(later.role == .off)
    }

    /// Turned on and off on the recording's clock: only what ends while attending enters the live log.
    @Test func periodsFollowTheRole() {
        var a = RecordingAttendance(role: .off)
        #expect(a.periods.isEmpty)
        a.set(.listener, at: 100)
        a.set(.participant, at: 150) // still attending: the same period
        a.set(.off, at: 200)
        a.set(.listener, at: 300)
        #expect(a.periods == [RecordingPeriod(start: 100, end: 200), RecordingPeriod(start: 300, end: nil)])
        #expect(!a.attended(line(40, 90)), "before it was turned on")
        #expect(a.attended(line(120, 140)))
        #expect(!a.attended(line(230, 250)), "while it was off")
        #expect(a.attended(line(310, 900)))
    }

    // MARK: The live log

    private func live(_ library: RecordingLibrary, open: RecordingSpan? = nil,
                      post: @escaping (UUID, Int, [RecordingSentence]) async throws -> MeetingLive.Answer = { _, from, s in .init(next: from + s.count) },
                      settled: Bool = true) -> MeetingLive {
        MeetingLive(deps: MeetingLive.Deps(post: post, settled: { _ in settled }, upload: { _ in }, openSpan: { _ in open },
                                           wait: { _ in }), library: library)
    }

    private func library() throws -> RecordingLibrary {
        RecordingLibrary(root: FileManager.default.temporaryDirectory.appending(path: "meeting-live-\(UUID())"))
    }

    /// What ended while attending and isn't Persona's voice joins the log once; positions never move after.
    @Test func theLiveLogIsAppendOnly() throws {
        let lib = try library()
        var e = meeting(.listener, lines: [line(1, 4, "a"), line(5, 9, "b"), line(10, 15, "Persona's own"), line(16, 20, "  ")])
        e.attendance?.spoken = [RecordingSpan(start: 10, end: 15)]
        try lib.save(e)
        let sender = live(lib)
        #expect(sender.append(e.id))
        #expect(lib.entry(e.id)?.attendance?.live.map(\.text) == ["a", "b"])
        #expect(!sender.append(e.id), "a segment is looked at once")
        // A later segment adds after; a re-transcription of the first changes nothing already given.
        lib.change(e.id) { x in
            x.segments.append(RecordingSegment(index: 1, start: 60, duration: 30, sentences: [line(61, 65, "c")]))
            x.segments[0].sentences = [line(1, 4, "a2")]
        }
        sender.append(e.id)
        #expect(lib.entry(e.id)?.attendance?.live.map(\.text) == ["a", "b", "c"])
    }

    /// A line Persona is saying right now (its stretch not closed yet) stays out too.
    @Test func theVoiceBeingSaidStaysOut() throws {
        let lib = try library()
        let e = meeting(.participant, lines: [line(1, 4, "question"), line(5, 12, "answer being said")])
        try lib.save(e)
        live(lib, open: RecordingSpan(start: 5, end: 12)).append(e.id)
        #expect(lib.entry(e.id)?.attendance?.live.map(\.text) == ["question"])
    }

    /// From where the hub is, in order; nothing before the hub has the attendance.
    @Test func theLogGoesFromTheHubsNext() async throws {
        let lib = try library()
        let e = meeting(.listener, lines: (0..<250).map { line(Double($0), Double($0) + 0.5, "l\($0)") })
        try lib.save(e)
        var sent: [(Int, Int)] = []
        let waiting = live(lib, post: { _, from, s in sent.append((from, s.count)); return .init(next: from + s.count) }, settled: false)
        waiting.append(e.id)
        await waiting.send(e.id)
        #expect(sent.isEmpty, "the upload naming the attendance goes first")

        let sender = live(lib, post: { _, from, s in sent.append((from, s.count)); return .init(next: from + s.count, fed: from) })
        await sender.send(e.id)
        #expect(sent.map(\.0) == [0, 200])
        #expect(sent.map(\.1) == [200, 50])
        #expect(sender.state(e.id).next == 250)
        await sender.send(e.id)
        #expect(sent.count == 2, "nothing more while the hub holds it all")
    }

    /// `409 live_gap`: the hub holds fewer than the phone thought; it asks where it is and goes on from there.
    @Test func aGapStartsAgainFromTheHub() async throws {
        let lib = try library()
        let e = meeting(.listener, lines: [line(1, 2, "a"), line(3, 4, "b"), line(5, 6, "c")])
        try lib.save(e)
        final class Hub { var held = 0; var calls: [Int] = [] }
        let hub = Hub()
        let sender = live(lib, post: { _, from, s in
            hub.calls.append(from)
            if from > hub.held { throw APIError(status: 409, message: "gap", fields: ["code": "live_gap"]) }
            hub.held = max(hub.held, from + s.count)
            return .init(next: hub.held)
        })
        sender.append(e.id)
        await sender.send(e.id)
        #expect(sender.state(e.id).next == 3)
        // The hub lost the last two (a restore); the phone still thinks it holds three, and has a fourth to send.
        hub.held = 1
        lib.change(e.id) { $0.segments.append(RecordingSegment(index: 1, start: 60, duration: 30, sentences: [self.line(61, 62, "d")])) }
        sender.append(e.id)
        await sender.send(e.id)
        #expect(hub.calls == [0, 3, 0], "from 3 is a gap; it asks again from the start and the hub skips what it holds")
        #expect(sender.state(e.id).next == 4)
        #expect(hub.held == 4)
    }

    /// An older manager (a 404 without `recording_not_found`): the meeting stops attending, the recording goes on.
    @Test func anOlderManagerStopsAttending() async throws {
        let lib = try library()
        let e = meeting(.listener, lines: [line(1, 2)])
        try lib.save(e)
        let sender = live(lib, post: { _, _, _ in throw APIError(status: 404, message: "not found") })
        sender.append(e.id)
        await sender.send(e.id)
        #expect(lib.entry(e.id)?.attendance?.stopped == .unsupported)
        #expect(lib.entry(e.id)?.attended == false)
    }

    // MARK: Asking

    /// A fake hub and voice: what was asked, what was said.
    private final class Calls {
        var asked: [MeetingAsk.Question] = []
        var said: [(String, UUID, String?)] = []
        var stopped = 0
        var maySpeak = true
        var heard: Int? = 7
        var clock = Date(timeIntervalSince1970: 1_800_000_000)
        var answer: Result<MeetingAsk.Answer, Error> = .success(.init(question: "Who owns the test?", text: "Bo owns it, due Friday.",
                                                                      lang: "en", speak: true, sources: ["Design review · Nov 3"]))
    }

    private func model(_ calls: Calls, now: Double = 312) -> MeetingAsk {
        MeetingAsk(deps: MeetingAsk.Deps(
            ask: { q in calls.asked.append(q); return try calls.answer.get() },
            now: { now },
            heard: { _ in calls.heard },
            settled: { _ in true },
            maySpeak: { _ in calls.maySpeak },
            speak: { text, exchange, _, lang, _ in calls.said.append((text, exchange, lang)) },
            stopSpeaking: { calls.stopped += 1 },
            clock: { calls.clock }))
    }

    private func settle(_ ask: MeetingAsk) async {
        for _ in 0..<100 where ask.busy { await Task.yield() }
    }

    /// One tap asks about what was just said: the lines up to the tap go first (`live_next`), the hub's understood question
    /// replaces "What was just said", and taking part the answer is said in the hub's language.
    @Test func aTapAsksAboutWhatWasJustSaid() async throws {
        let calls = Calls()
        let ask = model(calls)
        let e = meeting(.participant)
        let id = try #require(ask.ask(e))
        await settle(ask)
        let q = try #require(calls.asked.first)
        #expect(q.id == id)
        #expect(q.text == nil)
        #expect(q.at == 312)
        #expect(q.liveNext == 7)
        let done = try #require(ask.latest(for: e.id))
        #expect(done.answer == "Bo owns it, due Friday.")
        #expect(done.question == "Who owns the test?")
        #expect(done.sources == ["Design review · Nov 3"])
        #expect(calls.said.map(\.1) == [id])
        #expect(calls.said.first?.2 == "en")
    }

    /// A typed question goes as typed, without waiting for the live lines.
    @Test func aTypedQuestionGoesAsTyped() async throws {
        let calls = Calls()
        calls.answer = .success(.init(question: "What did we decide?", text: "Ship the cap.", lang: "en", speak: false, sources: []))
        let ask = model(calls)
        let e = meeting(.listener)
        _ = try #require(ask.ask(e, text: "  What did we decide?  "))
        await settle(ask)
        #expect(calls.asked.first?.text == "What did we decide?")
        #expect(calls.asked.first?.liveNext == nil)
        #expect(calls.said.isEmpty, "the hub said not to speak it: listening in")
    }

    /// The live bar's send button waits for words (#4486): spaces and new lines alone ask nothing, the same rule `ask` keeps.
    @Test func aTypedQuestionNeedsWords() {
        for blank in ["", " ", "\n", " \t\n  "] {
            #expect(!MeetingAsk.hasWords(blank), "\(blank.debugDescription) has no words")
        }
        for typed in ["?", "  Who owns the test?  ", "First line\nsecond line"] {
            #expect(MeetingAsk.hasWords(typed), "\(typed.debugDescription) can be asked")
        }
        let ask = model(Calls())
        #expect(ask.ask(meeting(.listener), text: " \n ") == nil, "nothing is asked for a blank question")
        #expect(ask.exchanges.isEmpty)
    }

    /// Said aloud only while the role still speaks and within a minute of the tap.
    @Test func anAnswerIsSaidOnlyWhenItMayBe() async throws {
        let calls = Calls()
        calls.maySpeak = false // switched to Listen while it was being written
        let ask = model(calls)
        _ = ask.ask(meeting(.participant))
        await settle(ask)
        #expect(calls.said.isEmpty)

        let late = Calls()
        let slow = model(late)
        late.answer = .success(.init(question: nil, text: "Late.", lang: nil, speak: true, sources: []))
        let e = meeting(.participant)
        _ = slow.ask(e)
        late.clock = late.clock.addingTimeInterval(61)
        await settle(slow)
        #expect(slow.latest(for: e.id)?.answer == "Late.", "shown")
        #expect(late.said.isEmpty, "not said after a minute")
    }

    @Test func nothingIsAskedWhenNobodyAttends() {
        let calls = Calls()
        let ask = model(calls)
        #expect(ask.ask(meeting(.off)) == nil)
        #expect(ask.ask(meeting(.listener), text: "   ") == nil)
        #expect(ask.exchanges.isEmpty)
        #expect(calls.asked.isEmpty)
    }

    @Test func aLongQuestionIsCutToTheContractsLimit() async throws {
        let calls = Calls()
        let ask = model(calls)
        _ = ask.ask(meeting(.listener), text: String(repeating: "問", count: MeetingAsk.maxQuestion + 50))
        await settle(ask)
        #expect(calls.asked.first?.text?.unicodeScalars.count == MeetingAsk.maxQuestion)
    }

    /// A failure says what happened; Retry asks the same question (the same id, the same moment).
    @Test func aFailedAskCanBeRetried() async throws {
        let calls = Calls()
        calls.answer = .failure(MeetingAsk.Failure.hub("nothing_heard"))
        let ask = model(calls)
        let e = meeting(.participant)
        let id = try #require(ask.ask(e))
        await settle(ask)
        #expect(ask.latest(for: e.id)?.state == .failed(MeetingAsk.message(MeetingAsk.Failure.hub("nothing_heard"))))
        calls.answer = .success(.init(question: nil, text: "Ship as is.", lang: nil, speak: true, sources: []))
        ask.retry(id, entry: e)
        await settle(ask)
        #expect(calls.asked.map(\.id) == [id, id])
        #expect(calls.asked[1].at == calls.asked[0].at)
        #expect(ask.latest(for: e.id)?.answer == "Ship as is.")
    }

    /// Each of the hub's reasons reads as its own sentence, an unknown one as the generic failure.
    @Test func everyReasonHasItsWords() {
        let generic = MeetingAsk.message(nil)
        for code in ["model_unavailable", "model_timeout", "interrupted", "nothing_heard", "ask_busy", "asks_full", "kb_read_only", "not_attending"] {
            #expect(MeetingAsk.message(MeetingAsk.Failure.hub(code)) != generic, "\(code)")
        }
        #expect(MeetingAsk.message(MeetingAsk.Failure.hub("something_new")) == generic)
        #expect(MeetingAsk.message(MeetingAsk.Failure.unsupported) != generic)
    }

    /// The request as the contract has it: a lowercase id, `live_next` only for a heard question; the answer's sources.
    @Test func theWireMatchesTheContract() throws {
        let id = UUID()
        let body = try JSONSerialization.jsonObject(with: JSON.encoder.encode(MeetingAskWire.body(
            .init(id: id, recordingID: UUID(), at: 12.5, text: nil, liveNext: 9)))) as! [String: Any]
        #expect(body["id"] as? String == id.uuidString.lowercased())
        #expect(body["question"] == nil)
        #expect(body["live_next"] as? Int == 9)
        let ask = try JSON.decoder.decode(MeetingAskWire.Ask.self, from: Data("""
            {"id":"x","question":"Q","question_source":"heard","state":"answered","answer":"A","lang":"zh-CN","speak":true,
             "sources":[{"kind":"recordings","id":"r","name":"Design review","when":"2026-11-03 14:00–15:10"},{"kind":"projects","id":"p","name":"Export"}]}
            """.utf8))
        let answer = MeetingAskWire.answer(ask)
        #expect(answer.sources == ["Design review · 2026-11-03 14:00–15:10", "Export"])
        #expect(answer.speak && answer.lang == "zh-CN")
    }

    /// The recording ended: the voice stops and its questions go.
    @Test func endingClearsTheMeeting() async throws {
        let calls = Calls()
        let ask = model(calls)
        let e = meeting(.participant), other = meeting(.listener)
        _ = ask.ask(e)
        _ = ask.ask(other, text: "Who?")
        await settle(ask)
        ask.ended(e.id)
        #expect(calls.stopped == 1)
        #expect(ask.exchanges(for: e.id).isEmpty)
        #expect(ask.exchanges(for: other.id).count == 1)
    }

    // MARK: Persona's own voice

    /// A line at least half inside a stretch Persona spoke in is its own, not the meeting's: out of the transcript and
    /// the upload.
    @Test func personasOwnVoiceIsLeftOutOfTheTranscript() throws {
        var e = meeting(.participant, lines: [line(2, 9, "Who owns the test?"), line(10.2, 17.8, "Bo owns the regression test."),
                                              line(18, 25, "Thanks, next item.")])
        #expect(e.sentences.count == 3)
        e.attendance?.spoken = [RecordingSpan(start: 10, end: 17.5)]
        #expect(e.sentences.map(\.text) == ["Who owns the test?", "Thanks, next item."])
        #expect(RecordingUpload(e).segments.map(\.text) == ["Who owns the test?", "Thanks, next item."])
    }

    @Test func aSpanHoldsLinesAtLeastHalfInsideIt() {
        let span = RecordingSpan(start: 10, end: 20)
        #expect(span.holds(line(10, 20)))
        #expect(span.holds(line(15, 25)), "exactly half")
        #expect(!span.holds(line(16, 30)))
        #expect(!span.holds(line(2, 11)))
    }

    @Test func theAnswerIsSaidInItsOwnLanguage() {
        #expect(MeetingSpeaker.language(of: "Bo owns the regression test, due Friday.", fallback: "zh-CN") == "en-US")
        #expect(MeetingSpeaker.language(of: "回归测试由张三负责，周五前完成。", fallback: "en-US") == "zh-CN")
        #expect(MeetingSpeaker.language(of: "回帰テストは金曜日までに張さんが担当します。", fallback: "zh-CN") == "ja-JP")
        #expect(MeetingSpeaker.language(of: "42", fallback: "ja-JP") == "ja-JP")
    }

    /// Persona's latest answer rides on the Live Activity, cut short; none when Persona doesn't attend.
    @Test func theActivityCarriesTheRoleAndTheLatestAnswer() {
        let e = meeting(.listener)
        let long = String(repeating: "答", count: 400)
        let c = RecordingActivity.content(entry: e, recording: true, interrupted: false, elapsed: 10, previous: nil, now: .now, answer: long)
        #expect(c.persona == "listener")
        #expect(c.answer?.count == RecordingActivity.answerLength)
        var off = meeting(.off)
        off.attendance = nil
        let plain = RecordingActivity.content(entry: off, recording: true, interrupted: false, elapsed: 10, previous: nil, now: .now, answer: "x")
        #expect(plain.persona == nil && plain.answer == nil)
    }
}
