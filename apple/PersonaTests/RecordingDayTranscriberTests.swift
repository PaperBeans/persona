import Foundation
import os
import Testing
@testable import Persona

private let anchor = Date(timeIntervalSince1970: 1_800_000_000) // 2027-01-15T08:00:00Z

private final class Clock: Sendable {
    private let value = OSAllocatedUnfairLock(initialState: anchor.addingTimeInterval(3600))
    var now: Date { value.withLock { $0 } }
    func advance(_ seconds: TimeInterval) { value.withLock { $0 = $0.addingTimeInterval(seconds) } }
}

private struct Unreadable: Error {}

/// The engine as the lane sees it: every call kept (the chunk files of each run), and each file answered with one sentence
/// named after it, or `failure` thrown.
private final class FakeEngine: Sendable {
    private let state = OSAllocatedUnfairLock(initialState: (calls: [[String]](), failure: Error?.none))

    var calls: [[String]] { state.withLock { $0.calls } }
    func fail(with error: Error?) { state.withLock { $0.failure = error } }

    func run(_ urls: [URL]) throws -> [(file: Int, sentence: RecordingSentence)] {
        let failure = state.withLock { state -> Error? in
            state.calls.append(urls.map(\.lastPathComponent))
            return state.failure
        }
        if let failure { throw failure }
        return urls.indices.map { (file: $0, sentence: RecordingSentence(start: 1, end: 2, text: urls[$0].lastPathComponent)) }
    }
}

/// An engine whose calls wait while it is held, and throw once their task is cancelled: a run caught in the middle.
/// Every call is kept with whether it could download a model.
private final class GatedEngine: Sendable {
    private let state = OSAllocatedUnfairLock(initialState: (calls: [(files: [String], downloads: Bool)](), held: true))

    var calls: [(files: [String], downloads: Bool)] { state.withLock { $0.calls } }
    func release() { state.withLock { $0.held = false } }

    func run(_ urls: [URL], downloads: Bool) async throws -> [(file: Int, sentence: RecordingSentence)] {
        state.withLock { $0.calls.append((urls.map(\.lastPathComponent), downloads)) }
        while true {
            try Task.checkCancellation()
            guard state.withLock({ $0.held }) else { break }
            try await Task.sleep(for: .milliseconds(2))
        }
        return urls.indices.map { (file: $0, sentence: RecordingSentence(start: 1, end: 2, text: urls[$0].lastPathComponent)) }
    }
}

/// The app's state as the lanes see it: in front or not, whether the system gives background time, and the grants it
/// gave, each of which can be made to run out.
@MainActor private final class FakeAppState: RecordingDayAppState {
    final class Grant: RecordingDayGrant {
        let onExpiry: @MainActor () -> Void
        private(set) var ended = false
        init(onExpiry: @escaping @MainActor () -> Void) { self.onExpiry = onExpiry }
        func end() { ended = true }
        func expire() {
            guard !ended else { return }
            onExpiry()
            ended = true
        }
    }

    var inForeground = true
    var givesGrants = true
    private(set) var grants: [Grant] = []

    func beginGrant(named name: String, onExpiry: @escaping @MainActor () -> Void) -> RecordingDayGrant? {
        guard givesGrants else { return nil }
        let grant = Grant(onExpiry: onExpiry)
        grants.append(grant)
        return grant
    }
}

/// Waits (briefly) until `condition` holds: a run reaching the engine happens on the lane's own task.
@MainActor private func until(_ condition: () -> Bool) async {
    var tries = 0
    while !condition(), tries < 2000 {
        tries += 1
        try? await Task.sleep(for: .milliseconds(1))
    }
    #expect(condition())
}

/// The days whose language change reached the upload (`RecordingDaySync.languageChanged`).
@MainActor private final class LanguageLog {
    var ids: [UUID] = []
}

/// One day in a temporary recordings folder, a store whose coalesced write never comes during a test, and a lane with
/// the fake engine (or a gated one), in front of the app unless `app` says otherwise.
@MainActor private final class Fixture {
    let root = FileManager.default.temporaryDirectory.appendingPathComponent("day-lane-\(UUID().uuidString)", isDirectory: true)
    let id = UUID()
    let clock = Clock()
    let engine = FakeEngine()
    let gate = GatedEngine()
    let app = FakeAppState()
    let library: RecordingLibrary
    let store: RecordingDayStore
    let lane: RecordingDayTranscriber
    let languages = LanguageLog()

    init(gated: Bool = false) {
        library = RecordingLibrary(root: root)
        let lib = library, clock = clock, engine = engine, gate = gate
        store = RecordingDayStore(root: root, isDeleted: { lib.isDeletedSnapshot($0) }, now: { clock.now }, writeDelay: 3600)
        let run: RecordingDayTranscriber.Engine
        if gated {
            run = { urls, _, _, _, downloads in try await gate.run(urls, downloads: downloads) }
        } else {
            run = { urls, _, _, _, _ in try engine.run(urls) }
        }
        let languages = languages
        lane = RecordingDayTranscriber(store: store, library: library, engine: run, clipsIdle: {}, now: { clock.now },
                                       appState: app, defaults: UserDefaults(suiteName: "day-lane-\(UUID().uuidString)")!,
                                       languageChanged: { languages.ids.append($0) })
    }

    private var events = 0

    /// Chunk `seq` as the watch sent it: `offset` seconds after `anchor`, 10 s, at `level` dBFS.
    func chunk(_ seq: Int, at offset: Double, level: Double = -30) throws {
        let header = RecordingDayHeader(recording: id, recordingStartedAt: anchor, tz: "Asia/Shanghai", day: "2027-01-15", seq: seq,
                                        startedAt: anchor.addingTimeInterval(offset), duration: 10, level: level)
        #expect(try store.store(header: header, audio: Data([UInt8(seq % 256), 1, 2])) == .stored)
    }

    /// The watch's next control event (numbered in the order the test sends them).
    func event(_ make: (Int) -> RecordingDayEvent) throws {
        let envelope = RecordingDayEventEnvelope(recording: id, recordingStartedAt: anchor, tz: "Asia/Shanghai", day: "2027-01-15",
                                                 event: make(events))
        events += 1
        #expect(try store.store(event: envelope) == .applied)
    }

    /// The day recording closed at `offset`: no chunk at or after `seqCount` comes.
    func close(_ seqCount: Int, at offset: Double) throws {
        try event { .closed($0, at: anchor.addingTimeInterval(offset), seqCount: seqCount) }
    }

    func record() async throws -> RecordingDayRecord { try #require(await store.record(id)) }
    func chunk(_ seq: Int) async throws -> RecordingDayChunk { try #require(try await record().chunks[seq]) }
    func cleanUp() { try? FileManager.default.removeItem(at: root) }
}

private func files(_ seqs: [Int]) -> [String] { seqs.map(RecordingSegment.fileName) }

/// All-day recording's transcription lane (docs/assistant/day-recap.md): silence skipped without the engine, contiguous
/// chunks read as one run, a run's failure counted against its chunks until they are `failed`, and a failure of the
/// device stopping the lane without counting.
@MainActor struct RecordingDayTranscriberTests {
    @Test func silentChunksAreSkippedWithoutTheEngine() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        try f.chunk(0, at: 0, level: -62)
        try f.chunk(1, at: 10, level: RecordingDayContract.silenceDBFS - 0.1)
        try f.close(2, at: 20)
        await f.lane.drain()
        #expect(f.engine.calls.isEmpty)
        for seq in [0, 1] {
            let chunk = try await f.chunk(seq)
            #expect(chunk.state == .skipped && chunk.sentences == [] && chunk.hasAudio)
        }
    }

    @Test func contiguousChunksAreOneRunEachSentenceOnTheChunkItStartsIn() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        for seq in 0..<3 { try f.chunk(seq, at: Double(seq) * 10) }
        try f.close(3, at: 30)
        await f.lane.drain()
        #expect(f.engine.calls == [files([0, 1, 2])], "one analyzer session for the three files")
        for seq in 0..<3 {
            let chunk = try await f.chunk(seq)
            #expect(chunk.state == .transcribed && chunk.attempts == 0)
            #expect(chunk.sentences == [RecordingSentence(start: 1, end: 2, text: RecordingSegment.fileName(seq))])
            #expect(chunk.positions == [RecordingDayPosition(part: 1, pos: seq)], "placed in the save, seq order")
        }
        #expect(try await f.record().parts.map(\.count) == [3], "part 1, made for them, not on the hub yet")
        #expect(f.lane.current == nil)
        // The text and its positions reach the disk together: the write that says transcribed holds them.
        #expect(await f.store.flush(f.id))
        let disk = try JSONDecoder().decode(RecordingDayRecord.self,
                                            from: Data(contentsOf: f.root.appendingPathComponent(f.id.uuidString).appendingPathComponent("day.json")))
        #expect(disk.chunks.values.allSatisfy { $0.state == .transcribed && $0.positions?.count == $0.sentences?.count })
    }

    @Test func aPauseEndsARunAndBreaksOneAcrossIt() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        // Chunk 1 starts 0.5 s after chunk 0 ended, close enough to join, but the user paused in between.
        try f.chunk(0, at: 0)
        try f.event { .paused($0, at: anchor.addingTimeInterval(10), reason: RecordingDayContract.Reason.paused) }
        try f.event { .started($0, at: anchor.addingTimeInterval(10.5)) }
        try f.chunk(1, at: 10.5)
        try f.chunk(2, at: 20.5)
        // The microphone is closed after chunk 2: its run is over at once, without waiting for a chunk to join.
        try f.event { .paused($0, at: anchor.addingTimeInterval(30.5), reason: RecordingDayContract.Reason.interrupted) }
        await f.lane.drain()
        #expect(f.engine.calls == [files([0]), files([1, 2])])
    }

    @Test func runsBreakAtGapsSilenceAndRunSeconds() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        try f.chunk(0, at: 0)
        try f.chunk(1, at: 10)
        try f.chunk(2, at: 20, level: -70)      // silence
        try f.chunk(3, at: 30)
        try f.chunk(4, at: 45)                  // 5 s after chunk 3 ended
        for seq in 5...12 { try f.chunk(seq, at: 55 + Double(seq - 5) * 10) }
        try f.close(13, at: 135)
        await f.lane.drain()
        #expect(f.engine.calls == [files([0, 1]), files([3]), files([4, 5, 6, 7, 8, 9]), files([10, 11, 12])],
                "at most runSeconds (60 s) per run")
        #expect(try await f.chunk(2).state == .skipped)
        #expect(try await f.record().chunks.values.filter(\.needsTranscription).isEmpty)
    }

    @Test func anOpenRunWaitsForMoreChunksThenGoesAsItIs() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        try f.chunk(0, at: 0)
        try f.chunk(1, at: 10)
        await f.lane.drain()
        #expect(f.engine.calls.isEmpty, "the newest chunk just came: the next may join the run")
        f.clock.advance(RecordingDayRuns.settleSeconds)
        await f.lane.drain()
        #expect(f.engine.calls == [files([0, 1])])

        // Planned from the record alone.
        let record = try await f.record()
        #expect(RecordingDayRuns.next(record, now: f.clock.now) == nil)
    }

    @Test func aFailedRunCountsItsTriesThenTheChunksAreUntranscribed() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        try f.chunk(0, at: 0)
        try f.chunk(1, at: 10)
        try f.chunk(2, at: 30)
        // Chunk 2 is the newest: its run waits for a chunk to join it unless none came for a while.
        f.clock.advance(RecordingDayRuns.settleSeconds)
        f.engine.fail(with: Unreadable())
        await f.lane.drain()
        // The pair once together, then each chunk alone until it has had `maxAttempts` tries.
        #expect(f.engine.calls == [files([0, 1]), files([0]), files([0]), files([1]), files([1]), files([2]), files([2]), files([2])])
        for seq in 0..<3 {
            let chunk = try await f.chunk(seq)
            #expect(chunk.state == .failed && chunk.attempts == RecordingDayContract.maxAttempts && chunk.sentences == nil)
            #expect(chunk.hasAudio, "kept for a retry until the 7-day cap")
        }
        // Reported as an untranscribed gap, never as silence; a later chunk is still transcribed.
        let record = try await f.record()
        #expect(RecordingDayGap.gaps(in: record, since: anchor, now: f.clock.now)
                    .contains(RecordingDayGap(from: 0, to: 20, reason: RecordingDayGap.Reason.untranscribed)))
        f.engine.fail(with: nil)
        try f.chunk(3, at: 60)
        try f.close(4, at: 70)
        await f.lane.drain()
        #expect(try await f.chunk(3).state == .transcribed)
        // The tries are on disk, not only in memory.
        #expect(await f.store.flush(f.id))
        let relaunched = RecordingDayStore(root: f.root, isDeleted: { _ in false })
        #expect(await relaunched.record(f.id)?.chunks[1]?.attempts == RecordingDayContract.maxAttempts)
    }

    @Test func aFailureOfTheDeviceStopsTheLaneWithoutCounting() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        try f.chunk(0, at: 0)
        try f.chunk(1, at: 30)
        try f.chunk(2, at: 60)
        try f.close(3, at: 70)
        f.engine.fail(with: RecordingSpeechError.noSpeechPermission)
        await f.lane.drain()
        #expect(f.engine.calls.count == 1, "one try shows speech recognition is off; the day's other runs aren't spent on it")
        #expect(f.lane.blocked == .noSpeechPermission)
        #expect(f.lane.hasWork)
        for seq in 0..<3 {
            let chunk = try await f.chunk(seq)
            #expect(chunk.state == .waiting && chunk.attempts == 0)
        }
        // In front again (the model may download now): the lane goes on.
        f.engine.fail(with: nil)
        f.lane.foreground()
        await f.lane.drain()
        #expect(f.lane.blocked == nil)
        for seq in 0..<3 { #expect(try await f.chunk(seq).state == .transcribed) }
    }

    /// No model for one day's language (or not installed while downloads are off) stops only the days in that language:
    /// today's chunks in another keep being transcribed and uploaded.
    @Test func aLanguageWithoutItsModelHoldsOnlyItsOwnDays() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        let engine = f.engine
        let lane = RecordingDayTranscriber(store: f.store, library: f.library, engine: { urls, _, locale, _, _ in
            if locale.identifier == "ja-JP" { throw RecordingSpeechError.modelUnavailable }
            return try engine.run(urls)
        }, clipsIdle: {}, now: { f.clock.now }, appState: f.app,
           defaults: UserDefaults(suiteName: "day-lane-\(UUID().uuidString)")!, languageChanged: { _ in })
        // An older day in Japanese, today's in English.
        f.library.adoptDay(id: f.id, day: "2027-01-15", timeZone: "Asia/Shanghai", createdAt: anchor)
        f.library.change(f.id) { $0.locale = "ja-JP" }
        try f.chunk(0, at: 0)
        try f.close(1, at: 10)
        let today = UUID()
        let start = anchor.addingTimeInterval(86_400)
        f.library.adoptDay(id: today, day: "2027-01-16", timeZone: "Asia/Shanghai", createdAt: start)
        f.library.change(today) { $0.locale = "en-US" }
        for seq in 0..<2 {
            let header = RecordingDayHeader(recording: today, recordingStartedAt: start, tz: "Asia/Shanghai", day: "2027-01-16",
                                            seq: seq, startedAt: start.addingTimeInterval(Double(seq) * 10), duration: 10, level: -30)
            #expect(try f.store.store(header: header, audio: Data([1, 2, 3])) == .stored)
        }
        let closed = RecordingDayEventEnvelope(recording: today, recordingStartedAt: start, tz: "Asia/Shanghai", day: "2027-01-16",
                                               event: .closed(0, at: start.addingTimeInterval(20), seqCount: 2))
        #expect(try f.store.store(event: closed) == .applied)

        await lane.drain()
        #expect(lane.blocked == nil, "the lane goes on")
        #expect(lane.blockedLanguages == ["ja-JP": .modelUnavailable])
        #expect(lane.hasWork, "the next catch-up is asked for")
        let english = try #require(await f.store.record(today))
        #expect(english.chunks.values.allSatisfy { $0.state == .transcribed }, "today's language has its model")
        let japanese = try await f.chunk(0)
        #expect(japanese.state == .waiting && japanese.attempts == 0, "no try counted against the waiting day")

        // In front again (the model may download now): the day in Japanese is tried again.
        lane.foreground()
        #expect(lane.blockedLanguages.isEmpty)
        await lane.drain()
        #expect(lane.blockedLanguages == ["ja-JP": .modelUnavailable], "still no model: still held, still not counted")
        #expect(try await f.chunk(0).attempts == 0)
    }

    /// A seq the watch still has (its live send failed and goes again): transcription stops at the gap, and goes past it
    /// only once it has been open `gapWaitSeconds`, so the late chunk is read in one run with its neighbours.
    @Test func transcriptionWaitsForAMissingSeqBeforeGoingPastIt() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        try f.chunk(0, at: 0)
        try f.chunk(1, at: 10)
        for seq in 3...8 { try f.chunk(seq, at: Double(seq) * 10) }
        f.clock.advance(RecordingDayRuns.settleSeconds)
        await f.lane.drain()
        #expect(f.engine.calls == [files([0, 1])], "up to the gap; 3…8 wait for 2 though they fill a run")
        let record = try await f.record()
        let opened = try #require(record.chunks[3]?.receivedAt)
        #expect(RecordingDayRuns.next(record, now: f.clock.now) == .wait(opened.addingTimeInterval(RecordingDayContract.gapWaitSeconds)))

        // Seq 2 comes: read with the chunks after it.
        try f.chunk(2, at: 20)
        try f.close(9, at: 90)
        await f.lane.drain()
        #expect(f.engine.calls == [files([0, 1]), files([2, 3, 4, 5, 6, 7]), files([8])])

        // Never coming: past the gap once it has been open two minutes.
        let g = Fixture()
        defer { g.cleanUp() }
        try g.chunk(0, at: 0)
        for seq in 2...7 { try g.chunk(seq, at: Double(seq) * 10) }
        await g.lane.drain()
        #expect(g.engine.calls == [files([0])], "up to the gap at once")
        g.clock.advance(RecordingDayContract.gapWaitSeconds)
        await g.lane.drain()
        #expect(g.engine.calls.last == files([2, 3, 4, 5, 6, 7]))
    }

    @Test func aChunkWhoseAudioWentIsUntranscribedNotSilent() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        try f.chunk(0, at: 0)
        try f.close(1, at: 10)
        await f.store.removeAudio(f.id, seqs: [0])
        await f.lane.drain()
        #expect(f.engine.calls.isEmpty)
        #expect(try await f.chunk(0).state == .failed)
    }

    @Test func anEraseReachesASentenceThatRanIntoItsAudio() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        // The writer paused 0.6 s between chunks 0 and 1, and their run joined across it.
        try f.chunk(0, at: 0)
        try f.chunk(1, at: 10.6)
        try f.chunk(2, at: 20.6)
        try f.close(3, at: 30.6)
        // Chunk 0's last sentence went on 0.3 s into chunk 1's audio, which on the wall clock is still before chunk 1
        // began; chunk 2's stays inside it. All of them are on the hub.
        let spanning = [RecordingSentence(start: 1, end: 3, text: "Inside."), RecordingSentence(start: 8, end: 10.3, text: "Into chunk 1.")]
        let id = f.id
        await f.store.update(id) { record in
            for seq in 0..<3 {
                let sentences = seq == 0 ? spanning : [RecordingSentence(start: 1, end: 2, text: "Own.")]
                record.chunks[seq]?.state = .transcribed
                record.chunks[seq]?.sentences = sentences
                record.chunks[seq]?.positions = sentences.indices.map { RecordingDayPosition(part: 1, pos: seq * 2 + $0) }
            }
            record.parts = [RecordingDayPart(hubID: id, part: 1, startedAt: anchor, created: true, base: 1, next: 6, count: 6)]
        }
        #expect(RecordingDayRuns.bleeding(try await f.record()).isEmpty)
        // The watch erased chunk 1's time: the wall clock alone misses chunk 0's last sentence.
        try f.event { .erase($0, id: UUID(), from: anchor.addingTimeInterval(10.6), to: anchor.addingTimeInterval(20.6), seqs: []) }
        let after = try await f.record()
        #expect(after.chunks[0]?.sentences == spanning && after.chunks[1]?.erased == true)
        #expect(RecordingDayRuns.bleeding(after) == [0])
        await f.lane.drain()
        #expect(f.engine.calls == [files([0])], "transcribed again alone, without the erased audio")
        let redone = try await f.chunk(0)
        // Its old sentences keep their hub positions with no text; the new one gets the next position.
        #expect(redone.sentences == [RecordingSentence(start: 1, end: 3, text: ""), RecordingSentence(start: 8, end: 10.3, text: ""),
                                     RecordingSentence(start: 1, end: 2, text: RecordingSegment.fileName(0))])
        #expect(redone.positions == [0, 1, 6].map { RecordingDayPosition(part: 1, pos: $0) })
        #expect(try await f.record().parts.first?.needsRepair == true, "the hub's copy is repaired with a PUT")
        #expect(try await f.chunk(2).sentences?.map(\.text) == ["Own."], "the chunk after the erase keeps its text")
        #expect(RecordingDayRuns.bleeding(try await f.record()).isEmpty, "an empty sentence holds no words: done")

        // Without its audio a chunk can't be read again: only the sentences inside it keep their text; one that reached
        // past keeps its position with none, or goes when it had none.
        let past = [RecordingSentence(start: 1, end: 3, text: "Kept."), RecordingSentence(start: 8, end: 10.3, text: "Past."),
                    RecordingSentence(start: 9, end: 10, text: "Up to the edge."), RecordingSentence(start: 9, end: 11, text: "Unplaced.")]
        var gone = RecordingDayChunk(seq: 0, startedAt: anchor, duration: 10, receivedAt: anchor, state: .transcribed, sentences: past,
                                     positions: [0, 1, 2].map { RecordingDayPosition(part: 2, pos: $0) })
        #expect(RecordingDayRuns.keepInside(&gone) == [2])
        #expect(gone.sentences?.map(\.text) == ["Kept.", "", "Up to the edge."])
        #expect(gone.positions?.count == 3)
    }

    @Test func howFarASentenceReachesIsCountedInTheRunsChunksNotTheWallClock() {
        // The run joined chunk 1 across a 0.6 s pause of the writer: a sentence 0.2 s past chunk 0's audio is in chunk 1's,
        // although the wall clock says chunk 1 began only later.
        func chunk(_ seq: Int, at offset: Double, erased: Bool = false, sentences: [RecordingSentence]? = nil) -> RecordingDayChunk {
            RecordingDayChunk(seq: seq, startedAt: anchor.addingTimeInterval(offset), duration: 10, receivedAt: anchor,
                              state: sentences == nil ? .waiting : .transcribed, sentences: sentences, hasAudio: !erased, erased: erased)
        }
        func record(_ chunks: [RecordingDayChunk]) -> RecordingDayRecord {
            RecordingDayRecord(id: UUID(), day: "2027-01-15", timeZone: "Asia/Shanghai", startedAt: anchor,
                               chunks: Dictionary(uniqueKeysWithValues: chunks.map { ($0.seq, $0) }))
        }
        let into = [RecordingSentence(start: 9, end: 10.2, text: "Across.")]
        #expect(RecordingDayRuns.bleeding(record([chunk(0, at: 0, sentences: into), chunk(1, at: 10.6, erased: true)])) == [0])
        // Through a whole chunk into the one after it.
        let far = [RecordingSentence(start: 9, end: 21, text: "Long.")]
        #expect(RecordingDayRuns.bleeding(record([chunk(0, at: 0, sentences: far), chunk(1, at: 10, sentences: []),
                                                  chunk(2, at: 20, erased: true)])) == [0])
        // Ending where its chunk's audio ends reaches nothing; nor does an erase before it, nor a sentence with no text.
        let edge = [RecordingSentence(start: 9, end: 10, text: "Edge.")]
        #expect(RecordingDayRuns.bleeding(record([chunk(0, at: 0, sentences: edge), chunk(1, at: 10, erased: true)])).isEmpty)
        #expect(RecordingDayRuns.bleeding(record([chunk(0, at: 0, erased: true), chunk(1, at: 10, sentences: into)])).isEmpty)
        let blank = [RecordingSentence(start: 9, end: 10.2, text: "")]
        #expect(RecordingDayRuns.bleeding(record([chunk(0, at: 0, sentences: blank), chunk(1, at: 10.6, erased: true)])).isEmpty)
    }

    @Test func aSentenceGoesToTheFileItStartsInOnItsChunksClock() {
        // Three 10 s chunks decoded to exactly their header lengths.
        let assigned = RecordingRun.assign([
            RecordingSentence(start: 0.5, end: 3, text: "a"),
            RecordingSentence(start: 9.8, end: 12.5, text: "b"),     // ends in the next file
            RecordingSentence(start: 10, end: 11, text: "c"),
            RecordingSentence(start: 25, end: 29, text: "d"),
            RecordingSentence(start: 28, end: 31, text: "e"),        // past the run's audio
        ], bounds: [0, 10, 20, 30], durations: [10, 10, 10])
        #expect(assigned.map { $0.file } == [0, 0, 1, 2, 2])
        #expect(assigned.map { $0.sentence } == [
            RecordingSentence(start: 0.5, end: 3, text: "a"),
            RecordingSentence(start: 9.8, end: 12.5, text: "b"),
            RecordingSentence(start: 0, end: 1, text: "c"),
            RecordingSentence(start: 5, end: 9, text: "d"),
            RecordingSentence(start: 8, end: 10, text: "e"),
        ])
        #expect(RecordingRun.assign([RecordingSentence(start: 1, end: 2, text: "x")], bounds: [0], durations: []).isEmpty)
    }

    @Test func aSentenceEndsPastItsChunkExactlyWhenItReachesTheNextFile() {
        // The decoder gave the first file 9.9 s where its header says 10: times map onto the header lengths.
        let bounds = [0, 9.9, 19.9], durations = [10.0, 10.0]
        let assigned = RecordingRun.assign([
            RecordingSentence(start: 1, end: 9.85, text: "inside"),      // in the first file's last 50 ms
            RecordingSentence(start: 2, end: 9.95, text: "across"),      // 50 ms into the second file
            RecordingSentence(start: 9.9, end: 10.9, text: "second"),
        ], bounds: bounds, durations: durations)
        let ends = assigned.map { $0.sentence.end }
        #expect(ends[0] < 10, "inside the first file stays inside its 10 s")
        #expect(ends[1] > 10 && abs(ends[1] - 10.05) < 1e-9, "a frame of the next file is past it")
        #expect(assigned[2].file == 1 && assigned[2].sentence.start == 0 && abs(ends[2] - 1) < 1e-9)
    }

    @Test func theClipLaneLeavesDaysAlone() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        f.library.adoptDay(id: f.id, day: "2027-01-15", timeZone: "Asia/Shanghai", createdAt: anchor, closed: true)
        let clips = RecordingTranscription(library: f.library) { _, _ in
            Issue.record("a day's chunks are never transcribed as a clip")
            return []
        }
        clips.enqueue(f.id)
        clips.resumePending()
        await clips.whenIdle()
        #expect(clips.current == nil)
    }

    @Test func leavingTheFrontWithoutAGrantStopsARunWithoutCountingAndNothingStartsUntilItIsBack() async throws {
        let f = Fixture(gated: true)
        defer { f.cleanUp() }
        try f.chunk(0, at: 0)
        try f.close(1, at: 10)
        f.lane.changed(f.id)
        await until { f.gate.calls.count == 1 }
        // The system gives no background time: the run stops now.
        f.app.inForeground = false
        f.app.givesGrants = false
        f.lane.background()
        await f.lane.whenIdle()
        var chunk = try await f.chunk(0)
        #expect(chunk.state == .waiting && chunk.attempts == 0, "a run cut short counts no try")
        #expect(f.lane.hasWork && f.lane.current == nil)
        // A change in the background without time to work starts nothing.
        f.lane.changed(f.id)
        await f.lane.whenIdle()
        #expect(f.gate.calls.count == 1)

        f.gate.release()
        f.app.inForeground = true
        f.lane.foreground()
        await f.lane.whenIdle()
        chunk = try await f.chunk(0)
        #expect(chunk.state == .transcribed && chunk.attempts == 0)
        #expect(f.gate.calls.map(\.downloads) == [true, true], "in front, a model may download")
    }

    @Test func aRunThatMayDownloadAModelIsCutShortInTheBackgroundAndGoesAgainWithout() async throws {
        let f = Fixture(gated: true)
        defer { f.cleanUp() }
        try f.chunk(0, at: 0)
        try f.close(1, at: 10)
        f.lane.changed(f.id)
        await until { f.gate.calls.count == 1 }
        f.app.inForeground = false
        f.lane.background()
        f.gate.release()
        await f.lane.whenIdle()
        #expect(f.gate.calls.map(\.downloads) == [true, false], "the same run again inside the grant, with no download")
        let chunk = try await f.chunk(0)
        #expect(chunk.state == .transcribed && chunk.attempts == 0)
        #expect(f.app.grants.count == 1 && f.app.grants.allSatisfy(\.ended), "the grant is given back when the run is done")
    }

    @Test func theCatchUpRunningOutLeavesNothingRunning() async throws {
        let f = Fixture(gated: true)
        defer { f.cleanUp() }
        f.app.inForeground = false
        f.app.givesGrants = false
        try f.chunk(0, at: 0)
        try f.chunk(1, at: 30)
        try f.close(2, at: 40)
        let catchUp = Task { await f.lane.drain() }
        await until { f.gate.calls.count == 1 }
        #expect(f.gate.calls[0].downloads == false, "never a download inside the catch-up")
        // The task's expiration handler cancels it.
        catchUp.cancel()
        await catchUp.value
        for _ in 0..<20 { await Task.yield() }
        #expect(f.gate.calls.count == 1, "no run starts after the task's time ran out")
        await f.lane.whenIdle()
        #expect(try await f.chunk(0).attempts == 0)
        #expect(f.lane.hasWork, "the next catch-up is asked for")
        #expect(f.app.grants.isEmpty, "and no background time was taken in its place")
    }

    /// The catch-up's time runs out while the lane waits for a clip still being transcribed (launched in the
    /// background, the app resumes pending clips first): the catch-up ends at once, so its task is completed in time.
    @Test func theCatchUpEndsInTimeWhileAClipIsStillTranscribing() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        f.app.inForeground = false
        f.app.givesGrants = false
        var clip = try f.library.create(source: .phone)
        clip.active = false; clip.expectedSegments = 1
        clip.segments = [RecordingSegment(index: 0, start: 0, duration: 30)]
        try f.library.save(clip)
        try Data([0]).write(to: f.library.audio(clip.id, 0))
        let held = OSAllocatedUnfairLock(initialState: true)
        let clips = RecordingTranscription(library: f.library) { _, _ in
            while held.withLock({ $0 }) { try? await Task.sleep(for: .milliseconds(2)) }
            return []
        }
        clips.enqueue(clip.id)
        await until { clips.current == clip.id }
        let safety = Task { try? await Task.sleep(for: .seconds(10)); held.withLock { $0 = false } }
        defer { safety.cancel() }
        let engine = f.engine
        let lane = RecordingDayTranscriber(store: f.store, library: f.library, engine: { urls, _, _, _, _ in try engine.run(urls) },
                                           clipsIdle: { await clips.whenIdle() }, now: { f.clock.now }, appState: f.app,
                                           defaults: UserDefaults(suiteName: "day-lane-\(UUID().uuidString)")!,
                                           languageChanged: { _ in })
        try f.chunk(0, at: 0)
        try f.close(1, at: 10)

        let catchUp = Task { await lane.drain() }
        for _ in 0..<50 { await Task.yield() }
        catchUp.cancel()
        await catchUp.value
        #expect(clips.current == clip.id, "ended while the clip was still being transcribed")
        await lane.whenIdle()
        #expect(f.engine.calls.isEmpty && lane.hasWork, "nothing of the day ran; the next catch-up is asked for")
        held.withLock { $0 = false }
        await clips.whenIdle()
    }

    @Test func aGrantRunningOutStopsTheRunAndNoOtherStartsInItsPlace() async throws {
        let f = Fixture(gated: true)
        defer { f.cleanUp() }
        f.app.inForeground = false
        try f.chunk(0, at: 0)
        try f.close(1, at: 10)
        // A chunk arriving in the background (a WatchConnectivity wake) gets a grant of its own.
        f.lane.changed(f.id)
        await until { f.gate.calls.count == 1 }
        #expect(f.app.grants.count == 1)
        f.app.givesGrants = false
        f.app.grants[0].expire()
        await f.lane.whenIdle()
        for _ in 0..<20 { await Task.yield() }
        #expect(f.gate.calls.count == 1)
        #expect(try await f.chunk(0).attempts == 0)
    }

    @Test func aNewLanguageTranscribesTheAudioAgainAndRepairsTheHubAlways() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        f.library.adoptDay(id: f.id, day: "2027-01-15", timeZone: "Asia/Shanghai", createdAt: anchor)
        try f.chunk(0, at: 0)
        try f.chunk(1, at: 10)
        try f.close(2, at: 20)
        await f.lane.drain()
        #expect(f.engine.calls == [files([0, 1])])
        let kept = try await f.chunk(1).sentences
        await f.store.removeAudio(f.id, seqs: [1])
        f.lane.setLocale(f.id, "fr-FR")
        await until { !f.languages.ids.isEmpty }
        #expect(f.languages.ids == [f.id], "the upload decides its repair PUT, text changed or not")
        #expect(f.library.entry(f.id)?.locale == "fr-FR")
        await f.lane.whenIdle()
        #expect(f.engine.calls == [files([0, 1]), files([0])], "the chunk whose audio is here is read again")
        let again = try await f.chunk(0)
        #expect(again.state == .transcribed)
        #expect(again.sentences?.map(\.text) == ["", RecordingSegment.fileName(0)], "the old text's position kept, empty")
        #expect(again.positions == [0, 2].map { RecordingDayPosition(part: 1, pos: $0) })
        #expect(try await f.chunk(1).sentences == kept, "the one whose audio is gone keeps its text")
        #expect(try await f.record().parts.first?.needsRepair == true)
        // The same language again changes nothing.
        f.lane.setLocale(f.id, "fr-FR")
        for _ in 0..<20 { await Task.yield() }
        await f.lane.whenIdle()
        #expect(f.languages.ids == [f.id] && f.engine.calls.count == 2)
    }
}
