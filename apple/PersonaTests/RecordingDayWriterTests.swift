import AVFoundation
import Foundation
import os
import Testing
@testable import Persona

private let anchor = Date(timeIntervalSince1970: 1_800_000_000) // 2027-01-15T08:00:00Z
private let rate = RecordingAudioSpec.sampleRate

/// 16 kHz mono audio made of runs of one value each (`(value, seconds)`), as the tap hands it to the writer.
private func audio(_ runs: [(value: Float, seconds: Double)]) throws -> AVAudioPCMBuffer {
    let format = try #require(AVAudioFormat(commonFormat: .pcmFormatFloat32, sampleRate: rate, channels: 1, interleaved: false))
    let counts = runs.map { Int(($0.seconds * rate).rounded()) }
    let buffer = try #require(AVAudioPCMBuffer(pcmFormat: format, frameCapacity: AVAudioFrameCount(counts.reduce(0, +))))
    let data = try #require(buffer.floatChannelData?[0])
    var at = 0
    for (run, count) in zip(runs, counts) {
        (data + at).update(repeating: run.value, count: count)
        at += count
    }
    buffer.frameLength = AVAudioFrameCount(at)
    return buffer
}

private func audio(_ value: Float, seconds: Double) throws -> AVAudioPCMBuffer { try audio([(value, seconds)]) }

/// A day recording's folder as the library names it: the recording's id.
private func folder(in root: URL) -> URL { root.appendingPathComponent(UUID().uuidString, isDirectory: true) }

private func id(of folder: URL) -> UUID? { UUID(uuidString: folder.lastPathComponent) }

private func samples(_ segment: RecordingSegmentWriter.Segment) -> Int { Int((segment.duration * rate).rounded()) }

private func files(in folder: URL) -> [String] {
    ((try? FileManager.default.contentsOfDirectory(atPath: folder.path)) ?? []).filter { $0.hasSuffix(".m4a") }.sorted()
}

/// Stands in for `RecordingLibrary.allocateSeq`: a counter per folder, starting where `first` says, that records each
/// call with whether the file of the seq it hands out exists yet.
private final class SeqCounters: Sendable {
    private let state: OSAllocatedUnfairLock<(next: [URL: Int], asked: [(folder: URL, seq: Int, fileExisted: Bool)])>

    init(first: [URL: Int] = [:]) { state = OSAllocatedUnfairLock(initialState: (first, [])) }

    var asked: [(folder: URL, seq: Int, fileExisted: Bool)] { state.withLock { $0.asked } }

    var allocate: @Sendable (URL) -> Int? {
        { [self] folder in
            state.withLock { counters in
                let seq = counters.next[folder] ?? 0
                counters.next[folder] = seq + 1
                let existed = FileManager.default.fileExists(atPath: folder.appendingPathComponent(RecordingSegment.fileName(seq)).path)
                counters.asked.append((folder, seq, existed))
                return seq
            }
        }
    }
}

/// Stands in for a writer event in `RecordingCapture.applyInOrder`: a name to follow it by, and the recordings it concerns.
private struct LoggedEvent {
    let name: String
    let owners: [UUID]
}

/// The writer of an all-day recording across midnight, fed without a microphone (`append`): which day every sample
/// lands in, the seq each chunk takes, the level of each chunk, and what it reports to whoever registers the chunks
/// (docs/assistant/day-recap.md).
struct RecordingDayWriterTests {
    private let root = FileManager.default.temporaryDirectory.appendingPathComponent("day-writer-\(UUID().uuidString)")

    /// A writer of 10-second chunks into a new folder, which exists (the library makes it with the recording).
    private func makeWriter(seqs: SeqCounters? = nil) throws -> (RecordingSegmentWriter, URL) {
        let today = folder(in: root)
        try FileManager.default.createDirectory(at: today, withIntermediateDirectories: true)
        let writer = RecordingSegmentWriter(directory: today, allocateSeq: seqs?.allocate)
        writer.segmentLength = RecordingDayContract.liveChunkSeconds
        writer.compactsFiles = true   // as the capture sets them for a day recording
        writer.cutsAtGaps = true
        return (writer, today)
    }

    @Test func midnightCutsOnTheExactSample() throws {
        defer { try? FileManager.default.removeItem(at: root) }
        let (writer, today) = try makeWriter()
        let tomorrow = folder(in: root)
        let midnight = anchor.addingTimeInterval(4)
        writer.setBoundary(at: midnight, nextDirectory: tomorrow)
        // One buffer from 4 s before midnight to 6 s after it.
        writer.append(try audio(0.1, seconds: 10), startedAt: anchor)

        let events = writer.takeEvents()
        #expect(events.count == 1)
        guard case .rotated(let rotation)? = events.first else {
            Issue.record("expected the midnight, got \(events)")
            return
        }
        let last = try #require(rotation.last, "the chunk open at midnight ends there")
        #expect(samples(last) == 64_000, "every sample before midnight and none after")
        #expect(last.owner == id(of: today) && last.index == 0 && last.startedAt == anchor)
        #expect(last.endedAt == midnight)
        #expect(rotation.previous == today && rotation.next == tomorrow)
        #expect(rotation.boundary == midnight && rotation.firstSampleAt == midnight)

        let first = try #require(writer.stop(), "the new day's open chunk")
        #expect(samples(first) == 96_000)
        #expect(first.owner == id(of: tomorrow) && first.index == 0 && first.start == 0)
        #expect(first.startedAt == midnight)
        // Both files hold their audio: the old one about 4 s, the new one about 6 s (AAC packets are 1024 samples).
        let before = try AVAudioFile(forReading: last.url).length
        let after = try AVAudioFile(forReading: first.url).length
        #expect(abs(before - 64_000) <= 2_048 && abs(after - 96_000) <= 2_048, "\(before) and \(after) samples")
    }

    @Test func aMidnightBetweenTwoSamplesGoesToTheLaterOne() throws {
        defer { try? FileManager.default.removeItem(at: root) }
        let tenSeconds = AVAudioFrameCount(10 * rate)
        let count = { (start: Date, boundary: Date) in RecordingSegmentWriter.frames(from: start, before: boundary, count: tenSeconds) }
        #expect(count(anchor, anchor.addingTimeInterval(4)) == 64_000)
        // Half a sample after sample 64 000: that sample is still before it.
        #expect(count(anchor, anchor.addingTimeInterval(4 + 0.5 / rate)) == 64_001)
        // The tiny error of subtracting two dates never moves the cut by a sample.
        #expect(count(anchor.addingTimeInterval(0.1), anchor.addingTimeInterval(4.1)) == 64_000)
        #expect(count(anchor, anchor.addingTimeInterval(-1)) == 0, "a midnight before the buffer: all of it is the next day's")
        #expect(count(anchor, anchor.addingTimeInterval(60)) == tenSeconds, "a midnight after it: none")

        let (writer, _) = try makeWriter()
        let midnight = anchor.addingTimeInterval(4 + 0.5 / rate)
        writer.setBoundary(at: midnight, nextDirectory: folder(in: root))
        writer.append(try audio(0.1, seconds: 10), startedAt: anchor)
        guard case .rotated(let rotation)? = writer.takeEvents().first else {
            Issue.record("expected the midnight")
            return
        }
        let last = try #require(rotation.last)
        #expect(samples(last) == 64_001)
        #expect(rotation.firstSampleAt == anchor.addingTimeInterval(64_001 / rate), "the next day starts at its first sample")
        let first = try #require(writer.stop())
        #expect(samples(first) == 160_000 - 64_001)
    }

    @Test func rotationThenStopKeepsEachDaysChunksApart() throws {
        defer { try? FileManager.default.removeItem(at: root) }
        let (writer, today) = try makeWriter()
        let tomorrow = folder(in: root)
        let reported = OSAllocatedUnfairLock(initialState: [String]())
        writer.onSegment = { segment in reported.withLock { $0.append("segment \(segment.index)") } }
        writer.onRotate = { last, next in reported.withLock { $0.append("midnight after \(last?.index ?? -1) into \(next.lastPathComponent)") } }
        writer.setBoundary(at: anchor.addingTimeInterval(25), nextDirectory: tomorrow)
        #expect(!FileManager.default.fileExists(atPath: tomorrow.path))

        writer.append(try audio(0.1, seconds: 30), startedAt: anchor)
        // Chunks 0 and 1 close by length, chunk 2 at midnight; the next day's chunk 0 is open.
        #expect(reported.withLock { $0 } == ["segment 0", "segment 1", "midnight after 2 into \(tomorrow.lastPathComponent)"])
        #expect(FileManager.default.fileExists(atPath: tomorrow.path), "the next day's folder is made before its first file")
        #expect(abs(writer.elapsed - 5) < 0.001, "the new day's timeline starts at midnight")

        let stopped = try #require(writer.stop())
        #expect(stopped.owner == id(of: tomorrow) && stopped.index == 0 && stopped.start == 0)
        #expect(stopped.startedAt == anchor.addingTimeInterval(25) && stopped.duration == 5)

        let finished = writer.finishedSegments
        #expect(finished.map(\.owner) == [today, today, today, tomorrow].map(id(of:)))
        #expect(finished.map(\.index) == [0, 1, 2, 0], "indexes start again with the new day")
        #expect(finished.map(\.startedAt) == [0, 10, 20, 25].map { anchor.addingTimeInterval($0) })
        #expect(finished.map(\.duration) == [10, 10, 5, 5])
        #expect(writer.finishedSegments.isEmpty, "taken once: a later stop or wake-up registers nothing again")
        #expect(files(in: today) == ["seg-000.m4a", "seg-001.m4a", "seg-002.m4a"])
        #expect(files(in: tomorrow) == ["seg-000.m4a"])
    }

    /// What capture already took is never reported again: a stop after a midnight has only the new day's tail, so the
    /// old day's chunks can't be registered a second time under the new day.
    @Test func stopAfterTheDayWasTakenReportsOnlyTheNewDay() throws {
        defer { try? FileManager.default.removeItem(at: root) }
        let (writer, today) = try makeWriter()
        let tomorrow = folder(in: root)
        writer.setBoundary(at: anchor.addingTimeInterval(25), nextDirectory: tomorrow)
        writer.append(try audio(0.1, seconds: 30), startedAt: anchor)

        let taken = writer.takeEvents()
        #expect(taken.count == 3)
        guard taken.count == 3, case .segment(let zero) = taken[0], case .segment(let one) = taken[1],
              case .rotated(let rotation) = taken[2] else {
            Issue.record("expected two chunks then the midnight, got \(taken)")
            return
        }
        #expect([zero, one].map(\.index) == [0, 1] && [zero, one].allSatisfy { $0.owner == id(of: today) })
        #expect(rotation.last?.index == 2 && rotation.last?.owner == id(of: today))

        let tail = try #require(writer.stop())
        #expect(writer.takeEvents() == [.segment(tail)])
        #expect(tail.owner == id(of: tomorrow) && tail.index == 0)
    }

    @Test func twoMidnightsInARow() throws {
        defer { try? FileManager.default.removeItem(at: root) }
        let (writer, today) = try makeWriter()
        let second = folder(in: root), third = folder(in: root)
        writer.setBoundary(at: anchor.addingTimeInterval(25), nextDirectory: second)
        writer.append(try audio(0.1, seconds: 30), startedAt: anchor)
        #expect(writer.takeEvents().count == 3)

        // The next midnight falls exactly where the second day's chunk 1 ends by length: no chunk is open there.
        writer.setBoundary(at: anchor.addingTimeInterval(45), nextDirectory: third)
        writer.append(try audio(0.1, seconds: 30), startedAt: anchor.addingTimeInterval(30))
        let events = writer.takeEvents()
        #expect(events.count == 4)
        guard events.count == 4, case .segment(let b0) = events[0], case .segment(let b1) = events[1],
              case .rotated(let rotation) = events[2], case .segment(let c0) = events[3] else {
            Issue.record("expected two chunks, the midnight, a chunk; got \(events)")
            return
        }
        #expect([b0, b1].map(\.index) == [0, 1] && [b0, b1].allSatisfy { $0.owner == id(of: second) })
        #expect(b1.endedAt == anchor.addingTimeInterval(45))
        #expect(rotation.last == nil && rotation.previous == second && rotation.next == third)
        #expect(rotation.firstSampleAt == anchor.addingTimeInterval(45))
        #expect(c0.owner == id(of: third) && c0.index == 0 && c0.startedAt == anchor.addingTimeInterval(45))
        let tail = try #require(writer.stop())
        #expect(tail.owner == id(of: third) && tail.index == 1)
        #expect(files(in: today).count == 3 && files(in: second).count == 2 && files(in: third).count == 2)
    }

    /// A day continued after a pause past midnight (no buffer came at midnight): the old day's last chunk ends where the
    /// audio stopped, and the new day starts at its first sample, so its date comes from that sample.
    @Test func audioBackAfterMidnightStartsTheDayAtItsFirstSample() throws {
        defer { try? FileManager.default.removeItem(at: root) }
        let (writer, today) = try makeWriter()
        let tomorrow = folder(in: root)
        writer.append(try audio(0.1, seconds: 15), startedAt: anchor)
        writer.setBoundary(at: anchor.addingTimeInterval(20), nextDirectory: tomorrow)
        writer.append(try audio(0.1, seconds: 5), startedAt: anchor.addingTimeInterval(3_600))

        let events = writer.takeEvents()
        #expect(events.count == 2)
        guard events.count == 2, case .rotated(let rotation) = events[1] else {
            Issue.record("expected a chunk then the midnight, got \(events)")
            return
        }
        let last = try #require(rotation.last)
        #expect(last.owner == id(of: today) && last.index == 1 && last.duration == 5)
        #expect(last.endedAt == anchor.addingTimeInterval(15))
        #expect(rotation.boundary == anchor.addingTimeInterval(20))
        #expect(rotation.firstSampleAt == anchor.addingTimeInterval(3_600))
        let first = try #require(writer.stop())
        #expect(first.owner == id(of: tomorrow) && first.index == 0 && first.startedAt == anchor.addingTimeInterval(3_600))
    }

    /// Each chunk takes its seq from its own recording's counter, asked once per file and before the file exists, so the
    /// seq is saved before any of its audio; across a midnight the next day's counter takes over. A day continued after
    /// a relaunch goes on where its counter is (docs/assistant/day-recap.md「The day recording on the watch」).
    @Test func eachChunkTakesItsSeqFromItsRecordingsCounterBeforeItsFileOpens() throws {
        defer { try? FileManager.default.removeItem(at: root) }
        let tomorrow = folder(in: root)
        let today = folder(in: root)
        try FileManager.default.createDirectory(at: today, withIntermediateDirectories: true)
        // Seqs 0…4 of today were handed out before (recorded, or reported lost by the launch's recovery).
        let seqs = SeqCounters(first: [today: 5])
        let writer = RecordingSegmentWriter(directory: today, allocateSeq: seqs.allocate)
        writer.segmentLength = RecordingDayContract.liveChunkSeconds
        writer.setBoundary(at: anchor.addingTimeInterval(25), nextDirectory: tomorrow)
        writer.append(try audio(0.1, seconds: 30), startedAt: anchor)
        writer.stop()

        let chunks = writer.finishedSegments
        #expect(chunks.map(\.index) == [5, 6, 7, 0])
        #expect(chunks.map(\.owner) == [today, today, today, tomorrow].map(id(of:)))
        #expect(seqs.asked.map { $0.folder } == [today, today, today, tomorrow], "one seq per file, from the file's folder")
        #expect(seqs.asked.map { $0.seq } == [5, 6, 7, 0])
        #expect(seqs.asked.allSatisfy { !$0.fileExisted }, "the seq is taken before its file is created")
        #expect(files(in: today) == ["seg-005.m4a", "seg-006.m4a", "seg-007.m4a"])
        #expect(files(in: tomorrow) == ["seg-000.m4a"])
    }

    @Test func levelIsEachChunksMeanPowerInDBFS() throws {
        defer { try? FileManager.default.removeItem(at: root) }
        let (writer, today) = try makeWriter()
        let tomorrow = folder(in: root)
        writer.append(try audio(0, seconds: 10), startedAt: anchor)
        writer.append(try audio(0.1, seconds: 10), startedAt: anchor.addingTimeInterval(10))
        // Half speech, half silence: the power average (−23 dB), not the average of the two levels.
        writer.append(try audio([(0.1, 5), (0, 5)]), startedAt: anchor.addingTimeInterval(20))
        // A loud chunk cut at midnight, and a silent one after it: each file counts only its own samples.
        writer.setBoundary(at: anchor.addingTimeInterval(35), nextDirectory: tomorrow)
        writer.append(try audio([(0.5, 5), (0, 5)]), startedAt: anchor.addingTimeInterval(30))
        writer.stop()

        let chunks = writer.finishedSegments
        #expect(chunks.map(\.owner) == [today, today, today, today, tomorrow].map(id(of:)))
        let expected = [RecordingDayLevel.floor, 20 * log10(0.1), 10 * log10(0.005), 20 * log10(0.5), RecordingDayLevel.floor]
        #expect(chunks.count == expected.count)
        for (chunk, level) in zip(chunks, expected) {
            #expect(abs(chunk.level - level) < 0.01, "chunk \(chunk.index) of \(chunk.owner?.uuidString ?? "?"): \(chunk.level) dBFS, expected \(level)")
        }
        #expect(chunks[0].level < RecordingDayContract.silenceDBFS && chunks[2].level > RecordingDayContract.silenceDBFS)
    }

    @Test func switchDirectoryCutsMovesOnAndDropsTheOldMidnight() throws {
        defer { try? FileManager.default.removeItem(at: root) }
        let next = folder(in: root), stale = folder(in: root)
        // Today's recording already had chunks up to 6: the writer goes on at its counter.
        let seqs = SeqCounters(first: [next: 7])
        let (writer, today) = try makeWriter(seqs: seqs)
        writer.append(try audio(0.1, seconds: 15), startedAt: anchor)
        writer.setBoundary(at: anchor.addingTimeInterval(100), nextDirectory: stale)

        let cut = try #require(writer.switchDirectory(to: next), "the open chunk is cut first")
        #expect(cut.owner == id(of: today) && cut.index == 1 && cut.duration == 5)
        #expect(FileManager.default.fileExists(atPath: next.path))
        #expect(writer.elapsed == 0, "a new timeline")

        // Audio past the old midnight no longer rotates: that midnight belonged to the day left behind.
        writer.append(try audio(0.1, seconds: 25), startedAt: anchor.addingTimeInterval(90))
        let tail = try #require(writer.stop())
        let events = writer.takeEvents()
        let segments = events.compactMap { event -> RecordingSegmentWriter.Segment? in
            if case .segment(let segment) = event { return segment }
            return nil
        }
        #expect(segments.count == events.count, "no midnight was crossed")
        #expect(segments.map(\.index) == [0, 1, 7, 8, 9])
        #expect(segments.map(\.owner) == [today, today, next, next, next].map(id(of:)))
        #expect(segments.suffix(3).map(\.start) == [0, 10, 20])
        #expect(segments.last == tail)
        #expect(!FileManager.default.fileExists(atPath: stale.path))
        #expect(files(in: next) == ["seg-007.m4a", "seg-008.m4a", "seg-009.m4a"])
    }

    /// A pause's `paused` event is timed where the audio stopped, not when the pause was handled.
    @Test func aPauseIsTimedWhereTheAudioEnded() throws {
        defer { try? FileManager.default.removeItem(at: root) }
        let (writer, _) = try makeWriter()
        #expect(writer.audioEnd == nil, "nothing recorded yet")
        writer.append(try audio(0.1, seconds: 15), startedAt: anchor)
        #expect(writer.audioEnd == anchor.addingTimeInterval(15))
        writer.cut()
        #expect(writer.audioEnd == anchor.addingTimeInterval(15), "the cut keeps where the audio ended")
        #expect(RecordingCapture.pauseTime(audioEnd: writer.audioEnd, now: anchor.addingTimeInterval(16)) == anchor.addingTimeInterval(15))
        #expect(RecordingCapture.pauseTime(audioEnd: nil, now: anchor) == anchor)
        #expect(RecordingCapture.pauseTime(audioEnd: anchor.addingTimeInterval(5), now: anchor) == anchor, "never in the future")
    }

    /// "Erase 10 min" cuts the open chunk and erases up to where its audio ends: the chunk cut falls in the range however
    /// short it is, and the chunk after it starts exactly there, so it keeps all of its audio (C5/C12).
    @Test func anEraseCutEndsWhereTheAudioEndsAndTheNextChunkStartsThere() throws {
        defer { try? FileManager.default.removeItem(at: root) }
        let (writer, today) = try makeWriter()
        writer.append(try audio(0.1, seconds: 15), startedAt: anchor)
        let cut = writer.cutForErase()
        let closed = try #require(cut.segment, "the open chunk is cut")
        #expect(closed.owner == id(of: today) && closed.index == 1 && closed.duration == 5)
        let through = try #require(cut.through)
        #expect(through == closed.endedAt && through == anchor.addingTimeInterval(15))
        #expect(writer.cutForErase() == RecordingSegmentWriter.Cut(segment: nil, through: through), "nothing open: nothing cut")

        // The next buffer's host-clock time says it began 20 ms before the cut's last sample ended.
        writer.append(try audio(0.1, seconds: 3), startedAt: through.addingTimeInterval(-0.02))
        let next = try #require(writer.stop())
        #expect(next.index == 2 && next.startedAt == through, "starts no earlier than the erased range ends")

        let window = try #require(RecordingCapture.eraseWindow(now: through.addingTimeInterval(0.5), audioEnd: through))
        #expect(window.to == through, "the range ends at the cut, not at the later clock reading")
        let record = { (segment: RecordingSegmentWriter.Segment) in
            RecordingSegment(index: segment.index, start: segment.start, duration: segment.duration, startedAt: segment.startedAt)
        }
        #expect(record(closed).overlapsDay(window.from, window.to))
        #expect(!record(next).overlapsDay(window.from, window.to))
    }

    /// A gap in the audio ends the day chunk being written: the input restarting after a route change keeps the writer
    /// and its open file, and a buffer the tap handed over after a pause's cut opens a file the continue would fill. The
    /// chunk's `started_at` and `duration` then still place every sample, so no gap falls inside one
    /// (docs/assistant/day-recap.md「Chunks」). The host clock's jitter is no gap.
    @Test func aGapInTheAudioEndsTheDayChunk() throws {
        defer { try? FileManager.default.removeItem(at: root) }
        let (writer, _) = try makeWriter()
        writer.append(try audio(0.1, seconds: 2), startedAt: anchor)
        // A buffer a few milliseconds off where the last one ended belongs to the same chunk.
        writer.append(try audio(0.1, seconds: 2), startedAt: anchor.addingTimeInterval(2.01))
        // The engine restarts: the next buffer comes a second later.
        writer.append(try audio(0.1, seconds: 2), startedAt: anchor.addingTimeInterval(5))
        // A buffer of just before a pause, then the continue half an hour later.
        writer.append(try audio(0.1, seconds: 0.1), startedAt: anchor.addingTimeInterval(7))
        writer.append(try audio(0.1, seconds: 3), startedAt: anchor.addingTimeInterval(1_807))
        writer.stop()

        let chunks = writer.finishedSegments
        #expect(chunks.map(\.index) == [0, 1, 2])
        #expect(chunks.map(\.startedAt) == [anchor, anchor.addingTimeInterval(5), anchor.addingTimeInterval(1_807)])
        let durations = chunks.map { ($0.duration * 1_000).rounded() / 1_000 }
        #expect(durations == [4, 2.1, 3], "each chunk holds its own audio, and only that: \(durations)")
        // Every other recording keeps one file across the gap, on its timeline without the pause.
        let other = folder(in: root)
        try FileManager.default.createDirectory(at: other, withIntermediateDirectories: true)
        let clip = RecordingSegmentWriter(directory: other)
        clip.append(try audio(0.1, seconds: 2), startedAt: anchor)
        clip.append(try audio(0.1, seconds: 2), startedAt: anchor.addingTimeInterval(60))
        #expect(clip.stop()?.duration == 4)
    }

    /// A finished chunk's rewrite without its padding keeps the file's creation date: when its first sample was
    /// written, which is where `recover` has a chunk an exit left unregistered begin.
    @Test func droppingThePaddingKeepsTheFilesCreationDate() throws {
        defer { try? FileManager.default.removeItem(at: root) }
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        let url = root.appendingPathComponent("chunk.m4a")
        let settings: [String: Any] = [AVFormatIDKey: kAudioFormatMPEG4AAC, AVSampleRateKey: rate, AVNumberOfChannelsKey: 1,
                                       AVEncoderBitRateKey: RecordingAudioSpec.bitRate]
        do {
            let file = try AVAudioFile(forWriting: url, settings: settings, commonFormat: .pcmFormatFloat32, interleaved: false)
            try file.write(from: try speech(seconds: 10))
        }
        let firstSample = anchor.addingTimeInterval(-10)
        try FileManager.default.setAttributes([.creationDate: firstSample], ofItemAtPath: url.path)
        let padded = try Data(contentsOf: url).count
        RecordingM4A.compact(url)
        #expect(try Data(contentsOf: url).count < padded, "rewritten")
        let created = try #require(try FileManager.default.attributesOfItem(atPath: url.path)[.creationDate] as? Date)
        #expect(abs(created.timeIntervalSince(firstSample)) < 0.001, "\(created)")
    }

    /// The floor an erase leaves only absorbs the host clock's jitter: a clock that really moved back keeps its time.
    @Test func anEraseCutKeepsAClockThatMovedBack() throws {
        defer { try? FileManager.default.removeItem(at: root) }
        let (writer, _) = try makeWriter()
        writer.append(try audio(0.1, seconds: 5), startedAt: anchor)
        let through = try #require(writer.cutForErase().through)
        writer.append(try audio(0.1, seconds: 2), startedAt: through.addingTimeInterval(-3))
        let next = try #require(writer.stop())
        #expect(next.startedAt == through.addingTimeInterval(-3))
    }

    /// A finished chunk carries no padding: 10 seconds of speech-like sound, with its header, fit the live envelope
    /// (docs/assistant/day-recap.md「Chunks」), and the file still reads back every sample.
    @Test func aTenSecondChunkFitsTheLiveEnvelope() throws {
        defer { try? FileManager.default.removeItem(at: root) }
        let (writer, today) = try makeWriter()
        writer.append(try speech(seconds: 10), startedAt: anchor)
        writer.stop()
        let segment = try #require(writer.finishedSegments.first)
        let data = try Data(contentsOf: segment.url)
        #expect(RecordingM4A.compacted(data) == nil, "nothing left to remove")
        let header = RecordingDayHeader(recording: try #require(id(of: today)), recordingStartedAt: anchor, tz: "Asia/Shanghai",
                                        day: "2027-01-15", seq: segment.index, startedAt: segment.startedAt,
                                        duration: segment.duration, level: segment.level)
        let envelope = RecordingDayOutbox.frameBytes(audio: data.count, header: header)
        #expect(RecordingDayOutbox.fitsMessage(envelope), "\(envelope) bytes")
        #expect(envelope <= RecordingDayOutbox.estimatedFrameBytes(seconds: segment.duration), "the estimate is generous")
        let file = try AVAudioFile(forReading: segment.url)
        #expect(abs(file.length - 160_000) <= 2_048, "\(file.length) samples")
    }

    /// Only all-day chunks are rewritten: any other recording's files stay exactly as AVAudioFile wrote them.
    @Test func otherRecordingsKeepTheirPadding() throws {
        defer { try? FileManager.default.removeItem(at: root) }
        let today = folder(in: root)
        try FileManager.default.createDirectory(at: today, withIntermediateDirectories: true)
        let writer = RecordingSegmentWriter(directory: today)
        writer.segmentLength = RecordingDayContract.liveChunkSeconds
        writer.append(try speech(seconds: 10), startedAt: anchor)
        writer.stop()
        let segment = try #require(writer.finishedSegments.first)
        #expect(RecordingM4A.compacted(try Data(contentsOf: segment.url)) != nil, "the padding is still there")
    }

    /// Dropping the padding moves each chunk offset back by what was removed before it, and decodes to the same samples.
    @Test func droppingThePaddingKeepsEverySample() throws {
        defer { try? FileManager.default.removeItem(at: root) }
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        let padded = root.appendingPathComponent("padded.m4a"), compact = root.appendingPathComponent("compact.m4a")
        let settings: [String: Any] = [AVFormatIDKey: kAudioFormatMPEG4AAC, AVSampleRateKey: rate, AVNumberOfChannelsKey: 1,
                                       AVEncoderBitRateKey: RecordingAudioSpec.bitRate]
        do {
            let file = try AVAudioFile(forWriting: padded, settings: settings, commonFormat: .pcmFormatFloat32, interleaved: false)
            try file.write(from: try speech(seconds: 10))
        }
        let original = try Data(contentsOf: padded)
        let smaller = try #require(RecordingM4A.compacted(original), "the writer pads its files")
        #expect(original.count - smaller.count > 10_000, "\(original.count) to \(smaller.count) bytes")
        try smaller.write(to: compact)
        #expect(try decoded(compact) == decoded(padded))
    }

    /// Only a layout it can rewrite is rewritten: no padding, or a chunk offset into the padding, leaves the file alone.
    @Test func droppingThePaddingNeedsALayoutItKnows() throws {
        func box(_ type: String, _ body: [UInt8]) -> [UInt8] {
            let size = UInt32(8 + body.count)
            return withUnsafeBytes(of: size.bigEndian, Array.init) + Array(type.utf8) + body
        }
        func movie(offset: UInt32) -> [UInt8] {
            let stco = box("stco", [0, 0, 0, 0] + withUnsafeBytes(of: UInt32(1).bigEndian, Array.init)
                + withUnsafeBytes(of: offset.bigEndian, Array.init))
            return box("moov", box("trak", box("mdia", box("minf", box("stbl", stco)))))
        }
        let ftyp = box("ftyp", Array("M4A ".utf8)), free = box("free", [UInt8](repeating: 0, count: 100))
        let moov = movie(offset: 0)
        // The first audio byte, after ftyp, moov, free and mdat's header.
        let first = UInt32(ftyp.count + moov.count + free.count + 8)
        let file = ftyp + movie(offset: first) + free + box("mdat", [1, 2, 3])
        let compacted = try #require(RecordingM4A.compacted(Data(file)))
        #expect(compacted == Data(ftyp + movie(offset: first - UInt32(free.count)) + box("mdat", [1, 2, 3])))
        #expect(RecordingM4A.compacted(compacted) == nil, "nothing to remove")
        let intoPadding = ftyp + movie(offset: UInt32(ftyp.count + moov.count + 4)) + free + box("mdat", [1, 2, 3])
        #expect(RecordingM4A.compacted(Data(intoPadding)) == nil)
        #expect(RecordingM4A.compacted(Data(file.dropLast())) == nil, "boxes that don't add up")
    }

    /// Speech-like sound over a little room noise, seeded: AAC doesn't squeeze it the way it squeezes one value.
    private func speech(seconds: Double) throws -> AVAudioPCMBuffer {
        let buffer = try audio(0, seconds: seconds)
        let data = try #require(buffer.floatChannelData?[0])
        var state: UInt64 = 1
        for i in 0..<Int(buffer.frameLength) {
            state = state &* 6_364_136_223_846_793_005 &+ 1_442_695_040_888_963_407
            let noise = Double(state >> 11) / Double(1 << 53) * 2 - 1
            let t = Double(i) / rate
            let voice = sin(2 * .pi * 140 * t) + 0.5 * sin(2 * .pi * 280 * t) + 0.25 * sin(2 * .pi * 420 * t)
            data[i] = Float(0.003 * noise + 0.06 * (1 - cos(2 * .pi * 4 * t)) * voice)
        }
        return buffer
    }

    private func decoded(_ url: URL) throws -> [Float] {
        let file = try AVAudioFile(forReading: url)
        let buffer = try #require(AVAudioPCMBuffer(pcmFormat: file.processingFormat, frameCapacity: AVAudioFrameCount(file.length)))
        try file.read(into: buffer)
        let data = try #require(buffer.floatChannelData?[0])
        return Array(UnsafeBufferPointer(start: data, count: Int(buffer.frameLength)))
    }
}

/// Capture's all-day decisions that need no microphone: which day a start goes on in, the day check, the erase window,
/// the seqs to report lost, the audio session events, the order writer events and control events are saved in.
struct RecordingDayCaptureRulesTests {
    private let newYork = TimeZone(identifier: "America/New_York")!
    private let london = TimeZone(identifier: "Europe/London")!

    private func utc(_ text: String) throws -> Date { try Date.ISO8601FormatStyle().parse(text) }

    private func day(_ day: String, in zone: TimeZone, createdAt: Date, source: RecordingEntry.Source = .watch) -> RecordingEntry {
        var entry = RecordingEntry(id: UUID(), createdAt: createdAt, source: source, kind: .day)
        entry.day = day
        entry.timeZone = zone.identifier
        return entry
    }

    @Test func dayCheckKeepsTheDayUntilItsOwnMidnightAcrossDST() throws {
        // 2026-11-01 has 25 hours in New York, 2026-03-08 has 23.
        let fallBack = try #require(RecordingDayCalendar.end(of: "2026-11-01", in: newYork))
        #expect(fallBack.timeIntervalSince(try utc("2026-11-01T04:00:00Z")) == 25 * 3_600)
        let springForward = try #require(RecordingDayCalendar.end(of: "2026-03-08", in: newYork))
        #expect(springForward.timeIntervalSince(try utc("2026-03-08T05:00:00Z")) == 23 * 3_600)

        let check = { (now: Date, running: Bool, planned: Date?) in
            RecordingCapture.dayCheck(day: "2026-11-01", zone: newYork, closed: false, now: now, running: running, plannedBoundary: planned)
        }
        #expect(check(try utc("2026-11-01T17:00:00Z"), true, nil) == .keep(boundary: fallBack))
        // An hour before the end of the 25-hour day, which a 24-hour count would already call tomorrow.
        #expect(check(try utc("2026-11-02T04:00:00Z"), false, fallBack) == .keep(boundary: fallBack))
    }

    @Test func dayCheckLetsTheWriterCutAtItsOwnMidnight() throws {
        let midnight = try #require(RecordingDayCalendar.end(of: "2026-11-01", in: newYork))
        let justAfter = midnight.addingTimeInterval(0.2)
        let check = { (day: String, now: Date, running: Bool, planned: Date?) in
            RecordingCapture.dayCheck(day: day, zone: newYork, closed: false, now: now, running: running, plannedBoundary: planned)
        }
        #expect(check("2026-11-01", justAfter, true, midnight) == .awaitWriter, "a buffer is due: the cut stays exact to the sample")
        #expect(check("2026-11-01", justAfter, false, midnight) == .switchNow, "paused past midnight: nothing else will move it")
        #expect(check("2026-11-01", justAfter, true, nil) == .switchNow)
        #expect(check("2026-11-01", midnight.addingTimeInterval(3 * 86_400), false, midnight) == .switchNow, "paused over several midnights")
        #expect(check("not a day", justAfter, true, nil) == .switchNow)
        #expect(RecordingCapture.dayCheck(day: "2026-11-01", zone: newYork, closed: true, now: justAfter.addingTimeInterval(-3_600),
                                          running: false, plannedBoundary: midnight) == .switchNow,
                "a day closed meanwhile (the transfer's midnight close) takes no more audio")
    }

    /// A day recording's day and midnight are in the zone it began in: flying east or west never ends it early or late
    /// (docs/assistant/day-recap.md「The day recording on the watch」).
    @Test func theRecordingsOwnZoneDecidesNotTheWatchs() throws {
        // Begun in New York on March 8; at 22:00 there it is already 02:00 on March 9 in London, where the watch is now.
        let newYorkMidnight = try #require(RecordingDayCalendar.end(of: "2026-03-08", in: newYork))
        let evening = try utc("2026-03-09T02:00:00Z")
        #expect(RecordingCapture.dayCheck(day: "2026-03-08", zone: newYork, closed: false, now: evening, running: true,
                                          plannedBoundary: newYorkMidnight) == .keep(boundary: newYorkMidnight))
        // Begun in London on March 9 and flown to New York, where it is still March 8: it ends at London's midnight.
        let londonMidnight = try utc("2026-03-10T00:00:00Z")
        #expect(RecordingCapture.dayCheck(day: "2026-03-09", zone: london, closed: false, now: try utc("2026-03-09T03:00:00Z"),
                                          running: true, plannedBoundary: nil) == .keep(boundary: londonMidnight))
        var flown = day("2026-03-09", in: london, createdAt: try utc("2026-03-09T00:30:00Z"))
        #expect(RecordingCapture.dayEnd(of: flown) == londonMidnight)
        flown.timeZone = "Not/AZone"
        #expect(RecordingCapture.dayEnd(of: flown) == nil)
    }

    /// A start goes on in the newest open day recording whose day is still on in its own zone, and closes the others
    /// that are over, each at its own midnight.
    @Test func aStartGoesOnInTheDayStillOnAndClosesThoseOver() throws {
        let yesterday = day("2026-10-31", in: newYork, createdAt: try utc("2026-10-31T12:00:00Z"))
        let today = day("2026-11-01", in: newYork, createdAt: try utc("2026-11-01T13:00:00Z"))
        // An hour before the end of the 25-hour day.
        let late = try utc("2026-11-02T04:00:00Z")
        let plan = RecordingCapture.dayToOpen([yesterday, today], now: late)
        #expect(plan.goingOn?.id == today.id)
        #expect(plan.closing.map { $0.id } == [yesterday.id])
        #expect(plan.closing.first?.at == RecordingDayCalendar.end(of: "2026-10-31", in: newYork), "at its own midnight")

        // At today's own midnight it is over too: the start makes a new recording.
        let midnight = try #require(RecordingDayCalendar.end(of: "2026-11-01", in: newYork))
        let next = RecordingCapture.dayToOpen([today], now: midnight)
        #expect(next.goingOn == nil && next.closing.map { $0.at } == [midnight])

        // A day in London still on while the watch's own date there moved on is continued, wherever the watch is.
        let abroad = day("2026-11-01", in: london, createdAt: try utc("2026-11-01T08:00:00Z"))
        #expect(RecordingCapture.dayToOpen([abroad], now: try utc("2026-11-01T23:30:00Z")).goingOn?.id == abroad.id)

        // One whose date can't be read is closed now; one begun later than now (the clock moved back) is left alone.
        var unreadable = day("2026-11-01", in: newYork, createdAt: try utc("2026-11-01T13:00:00Z"))
        unreadable.day = "someday"
        let ahead = day("2026-11-03", in: newYork, createdAt: try utc("2026-11-03T13:00:00Z"))
        let odd = RecordingCapture.dayToOpen([unreadable, ahead], now: late)
        #expect(odd.goingOn == nil)
        #expect(odd.closing.map { $0.id } == [unreadable.id] && odd.closing.first?.at == late)
    }

    @Test func eraseCoversTheLastTenMinutesUpToTheCut() throws {
        let now = anchor
        let window = try #require(RecordingCapture.eraseWindow(now: now, audioEnd: now.addingTimeInterval(-2)))
        #expect(window.from == now.addingTimeInterval(-RecordingRolling.eraseMinutes * 60) && window.to == now.addingTimeInterval(-2))
        #expect(RecordingCapture.eraseWindow(now: now, audioEnd: now.addingTimeInterval(3))?.to == now, "never past now")
        #expect(RecordingCapture.eraseWindow(now: now, audioEnd: now.addingTimeInterval(-11 * 60)) == nil,
                "paused for longer than the window: nothing recorded in it")
        #expect(RecordingCapture.eraseWindow(now: now, audioEnd: nil) == nil, "a writer that never had audio")
    }

    /// Just after midnight the window reaches into the day before, which is closed but still on the watch.
    @Test func anEraseJustAfterMidnightTouchesBothDays() throws {
        let midnight = try #require(RecordingDayCalendar.end(of: "2026-11-01", in: newYork))
        var yesterday = day("2026-11-01", in: newYork, createdAt: midnight.addingTimeInterval(-20 * 3_600))
        yesterday.active = false
        yesterday.closedAt = midnight
        let today = day("2026-11-02", in: newYork, createdAt: midnight)
        let phone = day("2026-11-02", in: newYork, createdAt: midnight, source: .phone)
        let clip = RecordingEntry(id: UUID(), createdAt: midnight, source: .watch)
        let entries = [today, yesterday, phone, clip]

        let across = RecordingErase(from: midnight.addingTimeInterval(-300), to: midnight.addingTimeInterval(300))
        #expect(Set(RecordingCapture.daysTouched(by: across, in: entries, source: .watch).map(\.id)) == [today.id, yesterday.id])
        let after = RecordingErase(from: midnight.addingTimeInterval(600), to: midnight.addingTimeInterval(1_200))
        #expect(RecordingCapture.daysTouched(by: after, in: entries, source: .watch).map(\.id) == [today.id])
        let before = RecordingErase(from: midnight.addingTimeInterval(-1_200), to: midnight.addingTimeInterval(-600))
        #expect(RecordingCapture.daysTouched(by: before, in: entries, source: .watch).map(\.id) == [yesterday.id])
    }

    /// The recording's clock keeps going wherever it can be seen: in front, and dimmed (Always On with the wrist down, a
    /// banner over the app), for a clip or a meeting as for all-day recording. Off screen it isn't written, and only
    /// all-day recording, hours there, ticks slower.
    @Test func theClockKeepsShowingWhileTheAppIsDimmed() {
        let quick = Duration.milliseconds(250)
        for allDay in [false, true] {
            #expect(RecordingCapture.tick(allDay: allDay, screen: .front) == .init(interval: quick, showsClock: true))
            #expect(RecordingCapture.tick(allDay: allDay, screen: .dimmed) == .init(interval: quick, showsClock: true))
        }
        #expect(RecordingCapture.tick(allDay: false, screen: .hidden) == .init(interval: quick, showsClock: false))
        #expect(RecordingCapture.tick(allDay: true, screen: .hidden) == .init(interval: .seconds(1), showsClock: false))
    }

    /// Seqs handed out with no chunk (a file that could not be opened or written) are reported lost once the writer has
    /// left the recording; acknowledged, erased or lost seqs and chunks still waiting for their save are not.
    @Test func seqsWithoutAChunkAreTheOnesToReportLost() {
        var entry = RecordingEntry(id: UUID(), createdAt: anchor, source: .watch, kind: .day)
        entry.prunedBelow = 3
        entry.prunedAbove = [4]
        entry.segments = [3, 6].map { RecordingSegment(index: $0, start: 0, duration: 10, startedAt: anchor) }
        #expect(RecordingCapture.holes(in: entry, allocated: 9, held: [7]) == [5, 8])
        #expect(RecordingCapture.holes(in: entry, allocated: 0, held: []) == [5], "the entry's own counter when it is higher")
        entry.segments = []
        entry.prunedBelow = 3
        entry.prunedAbove = nil
        #expect(RecordingCapture.holes(in: entry, allocated: 3, held: []).isEmpty, "every seq accounted for")
    }

    /// What the writer logged is applied in order; one that can't be saved holds everything after it that concerns any
    /// of its recordings (a midnight concerns both days), and the rest goes on. The next try starts with what was held.
    /// Owed control events follow the same rule per recording.
    @Test func writerEventsApplyInOrderAndHoldWhatFollowsAFailedSave() {
        let a = UUID(), b = UUID(), c = UUID()
        let log = [
            LoggedEvent(name: "a0", owners: [a]), LoggedEvent(name: "b0", owners: [b]), LoggedEvent(name: "a1", owners: [a]),
            LoggedEvent(name: "b1", owners: [b]), LoggedEvent(name: "midnight b→c", owners: [b, c]),
            LoggedEvent(name: "c0", owners: [c]), LoggedEvent(name: "a2", owners: [a]),
        ]
        var tried: [String] = []
        let first = RecordingCapture.applyInOrder(log, owners: { $0.owners }) { event in
            tried.append(event.name)
            return event.name == "b0" ? nil : event.owners
        }
        #expect(tried == ["a0", "b0", "a1", "a2"], "nothing after b0 that concerns b, or c through the midnight, is tried")
        #expect(first.changed == [a])
        #expect(first.kept.map(\.name) == ["b0", "b1", "midnight b→c", "c0"])

        tried = []
        let retry = RecordingCapture.applyInOrder(first.kept, owners: { $0.owners }) { event in
            tried.append(event.name)
            return event.owners
        }
        #expect(tried == ["b0", "b1", "midnight b→c", "c0"])
        #expect(retry.changed == [b, c] && retry.kept.isEmpty)
    }

    /// A day chunk counts as saved when its record is there, or when its seq is tombstoned (acknowledged, erased or
    /// reported lost meanwhile); anything else is a save that failed and is tried again. A clip's audio in an erased
    /// range is dropped on purpose.
    @Test func aDayChunkCountsAsSavedOnlyWhenItsRecordOrTombstoneIsThere() {
        let day = RecordingEntry(id: UUID(), createdAt: anchor, source: .watch, locale: "en-US", kind: .day)
        let chunk = RecordingSegment(index: 3, start: 30, duration: 10, startedAt: anchor.addingTimeInterval(30))
        #expect(!RecordingCapture.isSettled(chunk, in: day))
        #expect(!RecordingCapture.isSettled(chunk, in: nil))
        var saved = day
        saved.segments = [chunk]
        #expect(RecordingCapture.isSettled(chunk, in: saved))
        var tombstoned = day
        tombstoned.prunedAbove = [3]
        #expect(RecordingCapture.isSettled(chunk, in: tombstoned))
        var clip = day
        clip.kind = nil
        clip.erased = [RecordingErase(from: anchor, to: anchor.addingTimeInterval(60))]
        #expect(RecordingCapture.isSettled(chunk, in: clip))
    }

    @Test func sessionEventsPauseAndResumeOnlyWhatTheyShould() {
        let reaction = RecordingCapture.reaction
        #expect(reaction(.interrupted(reason: .default), true, false) == .pause, "a call during a recording takes the old began path")
        #expect(reaction(.interrupted(reason: nil), false, false) == .ignore, "nothing running: nothing to pause")
        #expect(reaction(.interruptionEnded(shouldResume: false), false, true) == .resume, "the meeting goes on whatever the advice")
        #expect(reaction(.interruptionEnded(shouldResume: true), false, false) == .ignore, "the user's own pause stays")
        for recording in [true, false] {
            for paused in [true, false] {
                #expect(reaction(.deactivatedByApp, recording, paused) == .ignore, "our own setActive(false) is no interruption")
                #expect(reaction(.activated, recording, paused) == .ignore)
            }
        }
    }
}

/// Capture's work on the library, on a temporary folder: the launch halt (C35), the watch's own erase, and the phone's
/// erase requests.
@MainActor struct RecordingDayCaptureLibraryTests {
    private let zone = "America/New_York"

    private func library() -> RecordingLibrary {
        RecordingLibrary(root: FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString))
    }

    private func day(_ lib: RecordingLibrary, createdAt: Date = anchor, day: String = "2027-01-15") throws -> UUID {
        try lib.create(source: .watch, kind: .day, createdAt: createdAt, day: day, timeZone: zone).id
    }

    /// Chunk `seq`, 10 s from `start` seconds after `anchor`, with its audio on disk.
    private func chunk(_ lib: RecordingLibrary, _ id: UUID, _ seq: Int, at start: Double) throws {
        try FileManager.default.createDirectory(at: lib.folder(id), withIntermediateDirectories: true)
        try Data([UInt8(seq)]).write(to: lib.audio(id, seq))
        lib.addSegment(id, RecordingSegment(index: seq, start: start, duration: 10, startedAt: anchor.addingTimeInterval(start), level: -30))
    }

    private func hasAudio(_ lib: RecordingLibrary, _ id: UUID, _ seq: Int) -> Bool {
        FileManager.default.fileExists(atPath: lib.audio(id, seq).path)
    }

    /// Registers the chunks a writer finished, as capture does with its events.
    private func register(_ events: [RecordingSegmentWriter.Event], in lib: RecordingLibrary, _ id: UUID) {
        for case .segment(let chunk) in events {
            lib.addSegment(id, RecordingSegment(index: chunk.index, start: chunk.start, duration: chunk.duration,
                                                startedAt: chunk.startedAt, level: chunk.level))
        }
    }

    @Test func aDayLeftOpenWithAllDayOnIsReportedAtLaunch() throws {
        let lib = library()
        defer { try? FileManager.default.removeItem(at: lib.root) }
        let yesterday = try lib.create(source: .watch, kind: .day, createdAt: anchor.addingTimeInterval(-86_400), day: "2027-01-14",
                                       timeZone: zone)
        let today = try lib.create(source: .watch, kind: .day, createdAt: anchor, day: "2027-01-15", timeZone: zone)
        let rolling = try lib.create(source: .watch, kind: .rolling, createdAt: anchor.addingTimeInterval(60))

        let recovered = lib.recover(source: .watch)
        #expect(Set(recovered) == [yesterday.id, today.id], "only day recordings stay open")
        #expect(lib.entry(rolling.id)?.active == false)
        #expect(lib.entry(today.id)?.gapReason == RecordingDayContract.Reason.stopped, "recovery says the microphone stopped")

        let halt = try #require(RecordingCapture.launchHalt(recovered: recovered, entries: lib.entries, intent: true))
        #expect(halt.id == today.id && halt.reason == .crashed)
        #expect(RecordingCapture.launchHalt(recovered: recovered, entries: lib.entries, intent: false) == nil,
                "all-day off: the user stopped for today")
        #expect(RecordingCapture.launchHalt(recovered: [], entries: lib.entries, intent: true) == nil)
        #expect(RecordingCapture.launchHalt(recovered: [rolling.id], entries: lib.entries, intent: true) == nil)
    }

    /// "Erase the last 10 minutes" four minutes after midnight, the day before closed at midnight and all of it already
    /// on the phone: that day is still on the watch, so it gets its own `erase` event for the minutes before midnight,
    /// and today's for those after (docs/assistant/day-recap.md「Erasing」: an erase that spans midnight applies to both
    /// day recordings). Only later does the settled day go.
    @Test func anEraseJustAfterMidnightReachesTheDayBeforeOnceThePhoneHasAllOfIt() throws {
        let lib = library()
        defer { try? FileManager.default.removeItem(at: lib.root) }
        let newYork = try #require(TimeZone(identifier: zone))
        let midnight = try #require(RecordingDayCalendar.end(of: "2027-01-15", in: newYork))
        let yesterday = try day(lib, createdAt: midnight.addingTimeInterval(-3_600))
        try chunk(lib, yesterday, 0, at: midnight.addingTimeInterval(-10).timeIntervalSince(anchor))
        let closed = try #require(lib.closeDay(yesterday, at: midnight))
        let today = try day(lib, createdAt: midnight, day: "2027-01-16")
        try chunk(lib, today, 0, at: midnight.timeIntervalSince(anchor))
        let ledger = RecordingDayWatchLedger(library: lib, recordingInto: today, holding: today)
        ledger.apply(RecordingDayAck(recording: yesterday, seqs: [0], events: [closed.n]))

        let now = midnight.addingTimeInterval(240)
        #expect(ledger.removeSettled(now: now).isEmpty, "the day before is settled, and kept")
        let window = try #require(RecordingCapture.eraseWindow(now: now, audioEnd: now))
        let touched = RecordingCapture.daysTouched(by: window, in: lib.entries, source: .watch).map(\.id)
        #expect(Set(touched) == [yesterday, today])
        for id in touched { #expect(RecordingCapture.erase(window, ofDay: id, in: lib)) }
        let before = try #require(lib.entry(yesterday)?.dayEvents?.last)
        #expect(before.type == .erase && before.from == window.from && before.seqs == [], "nothing of it left to delete")
        #expect(lib.entry(today)?.dayEvents?.last?.seqs == [[0, 0]])

        // Once that event is acknowledged too and the erase window has passed, the day before goes.
        ledger.apply(RecordingDayAck(recording: yesterday, events: [before.n]))
        #expect(ledger.removeSettled(now: midnight.addingTimeInterval(RecordingDayOutbox.keepClosedSeconds)) == [yesterday])
    }

    /// The watch's own erase (docs/assistant/day-recap.md「Erasing」): an `erase` event with its id, the range and the
    /// seqs of the chunks it deleted before the phone had them, saved before those chunks go. A chunk the phone already
    /// acknowledged is gone already and is not named.
    @Test func aWatchEraseSavesItsEventThenDeletesTheChunksItNames() throws {
        let lib = library()
        defer { try? FileManager.default.removeItem(at: lib.root) }
        let id = try day(lib)
        for seq in 0..<4 { try chunk(lib, id, seq, at: Double(seq) * 10) }
        lib.ackDayChunks(id, seqs: [0])
        let eventID = UUID()
        let range = RecordingErase(from: anchor.addingTimeInterval(5), to: anchor.addingTimeInterval(25))

        #expect(RecordingCapture.erase(range, ofDay: id, in: lib, eventID: eventID))
        let entry = try #require(lib.entry(id))
        let event = try #require(entry.dayEvents?.last)
        #expect(event.type == .erase && event.id == eventID && event.from == range.from && event.to == range.to)
        #expect(event.seqs == [[1, 2]], "the chunks in the range the phone had not acknowledged")
        #expect(entry.segments.map(\.index) == [3] && hasAudio(lib, id, 3), "the chunk after the range stays")
        #expect(!hasAudio(lib, id, 1) && !hasAudio(lib, id, 2))
        #expect(entry.isPruned(1) && entry.isPruned(2), "erased seqs are never used again")
        #expect(RecordingLibrary(root: lib.root).entry(id)?.dayEvents?.last == event, "on disk")

        // A range with nothing held left still tells the phone, which may hold chunks there.
        #expect(RecordingCapture.erase(range, ofDay: id, in: lib))
        #expect(lib.entry(id)?.dayEvents?.last?.seqs == [])
        #expect(!RecordingCapture.erase(range, ofDay: UUID(), in: lib), "no such day recording: nothing")
    }

    /// The phone's erase: the chunks held in the range go, and the answer names them; a repeated request (its answer
    /// was lost) gets the same answer and erases nothing more, and a recording the watch no longer holds is answered
    /// with no seqs.
    @Test func aPhoneEraseIsAnsweredOnceAndTheSameWayAgain() throws {
        let lib = library()
        defer { try? FileManager.default.removeItem(at: lib.root) }
        let id = try day(lib)
        for seq in 0..<3 { try chunk(lib, id, seq, at: Double(seq) * 10) }
        let request = RecordingDayEraseRequest(recording: id, id: UUID(), from: anchor.addingTimeInterval(5), to: anchor.addingTimeInterval(15))
        let now = anchor.addingTimeInterval(60)

        let answer = try #require(RecordingCapture.carryOut(request, in: lib, now: now))
        #expect(answer.recording == id && answer.erased == request.erase.id && answer.erasedSeqs == [0, 1])
        #expect(lib.entry(id)?.segments.map(\.index) == [2])
        #expect(lib.entry(id)?.dayEvents?.contains { $0.type == .erase } != true, "the answer, not an event, tells the phone")

        // A chunk registered in the range after the first answer (it was still being saved) stays for the phone to drop.
        try chunk(lib, id, 3, at: 12)
        #expect(RecordingCapture.carryOut(request, in: lib, now: now.addingTimeInterval(30)) == answer)
        #expect(lib.entry(id)?.segments.map(\.index) == [2, 3])

        let elsewhere = RecordingDayEraseRequest(recording: UUID(), id: UUID(), from: anchor, to: anchor.addingTimeInterval(10))
        #expect(RecordingCapture.carryOut(elsewhere, in: lib) == RecordingDayErased(recording: elsewhere.recording,
                                                                                   erased: elsewhere.erase.id, seqs: []))
    }

    /// A phone erase reaching into the chunk being written (a 60 s one, the phone away), which began inside the range:
    /// that chunk is cut first, so it is registered, erased and named in the answer, and none of the range's audio is
    /// finished later under a new seq. The open chunk of another recording, or one that begins where the range ends,
    /// holds none of the range and stays open.
    @Test func aPhoneEraseCutsTheOpenChunkItReachesIntoAndErasesIt() throws {
        let lib = library()
        defer { try? FileManager.default.removeItem(at: lib.root) }
        let id = try day(lib)
        try FileManager.default.createDirectory(at: lib.folder(id), withIntermediateDirectories: true)
        let writer = RecordingSegmentWriter(directory: lib.folder(id), allocateSeq: { @Sendable folder in
            UUID(uuidString: folder.lastPathComponent).map { lib.allocateSeq($0) }
        })
        writer.segmentLength = RecordingDayContract.liveChunkSeconds
        writer.append(try audio(0.1, seconds: 20), startedAt: anchor)
        writer.segmentLength = RecordingDayContract.backlogChunkSeconds
        writer.append(try audio(0.1, seconds: 30), startedAt: anchor.addingTimeInterval(20))
        register(writer.takeEvents(), in: lib, id)
        #expect(lib.entry(id)?.segments.map(\.index) == [0, 1], "chunk 2 is open from 20 s, 30 s into its 60")

        let request = RecordingDayEraseRequest(recording: id, id: UUID(), from: anchor.addingTimeInterval(15), to: anchor.addingTimeInterval(40))
        #expect(writer.cutOpenChunk(of: UUID(), startedBefore: request.erase.to) == nil, "another recording's chunk")
        #expect(writer.cutOpenChunk(of: id, startedBefore: anchor.addingTimeInterval(20)) == nil,
                "a range that ends where the chunk begins holds none of it")
        let cut = try #require(writer.cutOpenChunk(of: id, startedBefore: request.erase.to))
        #expect(cut.index == 2 && cut.startedAt == anchor.addingTimeInterval(20) && cut.duration == 30)
        register(writer.takeEvents(), in: lib, id)

        let answer = try #require(RecordingCapture.carryOut(request, in: lib))
        #expect(answer.erasedSeqs == [1, 2], "the chunk that was open is erased with the rest")
        #expect(lib.entry(id)?.segments.map(\.index) == [0])
        #expect(!hasAudio(lib, id, 1) && !hasAudio(lib, id, 2) && hasAudio(lib, id, 0))

        // The audio after the cut opens a new chunk, which begins after the range.
        writer.append(try audio(0.1, seconds: 5), startedAt: anchor.addingTimeInterval(50))
        let next = try #require(writer.stop())
        #expect(next.index == 3 && next.startedAt == anchor.addingTimeInterval(50))
    }
}
