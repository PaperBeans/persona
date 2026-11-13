import Foundation
import Testing
@testable import Persona

private let start = Date(timeIntervalSince1970: 1_800_000_000) // 2027-01-15T08:00:00Z
private typealias Reason = RecordingDayGap.Reason

/// Chunk `seq` `offset` seconds into the day as the phone holds it. A transcribed chunk gets one sentence per position
/// in `positions` (of `part`) unless `sentences` says otherwise; it arrived `received` seconds after `start` (its end by
/// default).
private func chunk(_ seq: Int, at offset: Double, duration: Double = 10, state: RecordingDayChunk.State = .transcribed,
                   sentences: Int? = nil, positions: [Int] = [], part: Int = 1, erased: Bool = false, lost: Bool = false,
                   received: Double? = nil) -> RecordingDayChunk {
    let count = sentences ?? positions.count
    let text = state == .transcribed ? (0..<count).map { RecordingSentence(start: Double($0) + 1, end: Double($0) + 2, text: "s\($0)") } : nil
    return RecordingDayChunk(seq: seq, startedAt: start.addingTimeInterval(offset), duration: duration, level: -30,
                             receivedAt: start.addingTimeInterval(received ?? offset + duration), state: state, sentences: text,
                             hasAudio: !erased && !lost, positions: positions.isEmpty ? nil : positions.map { RecordingDayPosition(part: part, pos: $0) },
                             erased: erased, lost: lost)
}

private func day(_ chunks: [RecordingDayChunk], lost: Set<Int> = [], losses: [RecordingDayLoss] = [],
                 erasures: [RecordingDayErasure] = [], pauses: [RecordingDayPause] = [], closedAt: Double? = nil,
                 seqCount: Int? = nil, parts: [RecordingDayPart] = []) -> RecordingDayRecord {
    RecordingDayRecord(id: UUID(), day: "2027-01-15", timeZone: "Asia/Shanghai", startedAt: start,
                       chunks: Dictionary(uniqueKeysWithValues: chunks.map { ($0.seq, $0) }), lost: lost, losses: losses,
                       erasures: erasures, pauses: pauses, closedAt: closedAt.map { start.addingTimeInterval($0) },
                       seqCount: seqCount, parts: parts)
}

private func part(_ number: Int = 1, count: Int, next: Int? = nil, endedAt: Double? = nil) -> RecordingDayPart {
    RecordingDayPart(hubID: UUID(), part: number, startedAt: start, endedAt: endedAt.map { start.addingTimeInterval($0) },
                     created: true, base: 1, next: next ?? count, count: count)
}

private func at(_ offset: Double) -> Date { start.addingTimeInterval(offset) }

/// The coverage the phone sends with every create, repair and append (docs/assistant/day-recap.md「The recording」): `to`
/// never passes text the hub won't hold after the request, the gaps say why time has no text, and `complete` waits
/// for the watch's `closed` and every seq below its count.
struct RecordingDayCoverageTests {
    private let now = start.addingTimeInterval(3600)

    @Test func toStopsAtTextTheHubDoesNotHaveYet() {
        let record = day([chunk(0, at: 0, positions: [0, 1]), chunk(1, at: 10, positions: [2]), chunk(2, at: 20, positions: [3])])
        let p = part(count: 4)
        #expect(RecordingDayCoverage.compute(record: record, part: p, sentToPositionBelow: 0, now: now).to == 0)
        #expect(RecordingDayCoverage.compute(record: record, part: p, sentToPositionBelow: 2, now: now).to == 10)
        #expect(RecordingDayCoverage.compute(record: record, part: p, sentToPositionBelow: 3, now: now).to == 20,
                "chunk 2's sentence is at position 3, past what this request leaves on the hub")
        #expect(RecordingDayCoverage.compute(record: record, part: p, sentToPositionBelow: 4, now: now).to == 30)

        // Transcribed text without a position yet, or a chunk still waiting, holds `to` back whatever the count.
        let unplaced = day([chunk(0, at: 0, positions: [0]), chunk(1, at: 10, sentences: 2, positions: [1]), chunk(2, at: 20, positions: [3])])
        #expect(RecordingDayCoverage.compute(record: unplaced, part: p, sentToPositionBelow: 100, now: now).to == 10)
        let waiting = day([chunk(0, at: 0, positions: [0]), chunk(1, at: 10, state: .waiting), chunk(2, at: 20, positions: [1])])
        #expect(RecordingDayCoverage.compute(record: waiting, part: p, sentToPositionBelow: 100, now: now).to == 10)
    }

    @Test func settledChunksCountAsCoveredAndReportTheirGap() {
        let record = day([
            chunk(0, at: 0, state: .skipped),
            chunk(1, at: 10, state: .waiting, erased: true),
            chunk(2, at: 20, state: .waiting, lost: true),
            chunk(3, at: 30, state: .failed),
            chunk(4, at: 40, positions: [0]),
        ], erasures: [RecordingDayErasure(id: UUID(), from: at(10), to: at(20))])
        let coverage = RecordingDayCoverage.compute(record: record, part: part(count: 1), sentToPositionBelow: 1, now: now)
        #expect(coverage.to == 50)
        #expect(coverage.gaps == [
            RecordingDayGap(from: 10, to: 20, reason: Reason.erased),
            RecordingDayGap(from: 20, to: 30, reason: Reason.lost),
            RecordingDayGap(from: 30, to: 40, reason: Reason.untranscribed),
        ], "silence is no gap; an untranscribed chunk is one [ext], not silence")
    }

    @Test func aMissingSeqHoldsToUntilTheChunkAfterItHasWaited() {
        // Seq 1 is still on the watch; seq 2 came 40 s after the start.
        let held = [chunk(0, at: 0, positions: [0]), chunk(2, at: 20, positions: [1], received: 40)]
        let record = day(held)
        let p = part(count: 2)
        let waiting = RecordingDayCoverage.compute(record: record, part: p, sentToPositionBelow: 2,
                                                   now: at(40 + RecordingDayContract.gapWaitSeconds - 1))
        #expect(waiting.to == 10)
        #expect(waiting.gaps == [RecordingDayGap(from: 10, to: 20, reason: Reason.pending)])
        // Measured from the persisted arrival, so a relaunch doesn't start the two minutes over.
        let passed = RecordingDayCoverage.compute(record: record, part: p, sentToPositionBelow: 2,
                                                  now: at(40 + RecordingDayContract.gapWaitSeconds))
        #expect(passed.to == 30)
        #expect(passed.gaps.map(\.reason) == [Reason.pending], "still pending: the audio may yet come")
        #expect(RecordingDayCoverage.nextGapExpiry(record: record, now: at(50)) == at(40 + RecordingDayContract.gapWaitSeconds))
        #expect(RecordingDayCoverage.nextGapExpiry(record: record, now: at(400)) == nil)

        // A seq the watch reported lost, or one an erase deleted before the phone had it, never holds it back.
        let lost = day(held, lost: [1], losses: [RecordingDayLoss(fromSeq: 1, toSeq: 1, from: nil, to: nil)])
        let reported = RecordingDayCoverage.compute(record: lost, part: p, sentToPositionBelow: 2, now: at(41))
        #expect(reported.to == 30)
        #expect(reported.gaps == [RecordingDayGap(from: 10, to: 20, reason: Reason.lost)])
        var erased = day(held, erasures: [RecordingDayErasure(id: UUID(), from: at(10), to: at(20))])
        erased.erasedSeqs = [1]
        #expect(RecordingDayCoverage.compute(record: erased, part: p, sentToPositionBelow: 2, now: at(41)).to == 30)
        #expect(RecordingDayCoverage.nextGapExpiry(record: erased, now: at(41)) == nil)
    }

    @Test func theCloseCarriesToToTheEndWithThePauseBeforeIt() {
        // Recorded until 10 s, stopped by itself, the day closed an hour in with seq_count 1.
        let record = day([chunk(0, at: 0, positions: [0])],
                         pauses: [RecordingDayPause(from: at(10), to: at(3600), reason: RecordingDayContract.Reason.stopped)],
                         closedAt: 3600, seqCount: 1)
        let p = part(count: 1)
        let coverage = RecordingDayCoverage.compute(record: record, part: p, sentToPositionBelow: 1, now: now)
        #expect(coverage.to == 3600)
        #expect(coverage.gaps == [RecordingDayGap(from: 10, to: 3600, reason: RecordingDayContract.Reason.stopped)])
        #expect(RecordingDayCoverage.isComplete(record: record, part: p, sentToPositionBelow: 1, coverage: coverage))

        // While open, a pause the watch hasn't ended runs to now, and `to` stays at the last audio: a chunk cut by the
        // pause may still be on its way.
        let open = day([chunk(0, at: 0, positions: [0])],
                       pauses: [RecordingDayPause(from: at(10), to: nil, reason: RecordingDayContract.Reason.interrupted)])
        let paused = RecordingDayCoverage.compute(record: open, part: p, sentToPositionBelow: 1, now: now)
        #expect(paused.to == 10)
        #expect(paused.gaps == [RecordingDayGap(from: 10, to: 3600, reason: RecordingDayContract.Reason.interrupted)])
        #expect(!RecordingDayCoverage.isComplete(record: open, part: p, sentToPositionBelow: 1, coverage: paused))

        // Closed with a seq still on the watch: pending until it comes, so neither `to` nor `complete` gets there.
        let behind = day([chunk(0, at: 0, positions: [0])], closedAt: 3600, seqCount: 2)
        let short = RecordingDayCoverage.compute(record: behind, part: p, sentToPositionBelow: 1, now: now)
        #expect(short.to == 10)
        #expect(short.gaps == [RecordingDayGap(from: 10, to: 3600, reason: Reason.pending)])
        #expect(!RecordingDayCoverage.isComplete(record: behind, part: p, sentToPositionBelow: 1, coverage: short))
    }

    @Test func completeNeedsTheCloseEverySeqAndEverySentenceOnTheHub() {
        let p = part(count: 1)
        func complete(_ record: RecordingDayRecord, sent: Int = 1) -> Bool {
            let coverage = RecordingDayCoverage.compute(record: record, part: p, sentToPositionBelow: sent, now: now)
            return RecordingDayCoverage.isComplete(record: record, part: p, sentToPositionBelow: sent, coverage: coverage)
        }
        let first = chunk(0, at: 0, positions: [0])
        #expect(complete(day([first, chunk(1, at: 10, state: .skipped)], closedAt: 20, seqCount: 2)), "silence is settled text")
        #expect(!complete(day([first, chunk(1, at: 10, state: .skipped)], closedAt: 20, seqCount: 2), sent: 0),
                "its text isn't on the hub yet")
        #expect(!complete(day([first, chunk(1, at: 10, state: .skipped)])), "not closed")
        #expect(!complete(day([first, chunk(1, at: 10, state: .failed)], closedAt: 20, seqCount: 2)),
                "never transcribed: the hub's cutoff recap covers such a day")
        #expect(!complete(day([first, chunk(1, at: 10, state: .waiting)], closedAt: 20, seqCount: 2)), "not transcribed yet")
        #expect(!complete(day([first], closedAt: 20, seqCount: 2)), "seq 1 is still pending")
        #expect(complete(day([first], lost: [1], closedAt: 20, seqCount: 2)), "reported lost")
        var erased = day([first], erasures: [RecordingDayErasure(id: UUID(), from: at(10), to: at(20))], closedAt: 20, seqCount: 2)
        erased.erasedSeqs = [1]
        #expect(complete(erased), "erased on the watch before the phone had it")
        #expect(complete(day([first, chunk(1, at: 10, state: .waiting, erased: true)], closedAt: 20, seqCount: 2)))
    }

    @Test func aPartClosedEarlyReachesItsEndAndCompletesWithTheDay() {
        // Part 1 filled up at 20 s (413 day_full); part 2 holds what came after, on the same clock.
        let chunks = [
            chunk(0, at: 0, positions: [0]), chunk(1, at: 10, positions: [1]),
            chunk(2, at: 20, positions: [0], part: 2), chunk(3, at: 30, positions: [1], part: 2),
            chunk(4, at: 45, positions: [2], part: 2),
        ]
        let first = part(1, count: 2, endedAt: 20)
        let second = part(2, count: 3)
        let pause = RecordingDayPause(from: at(40), to: at(45), reason: RecordingDayContract.Reason.interrupted)
        let record = day(chunks + [chunk(5, at: 60, state: .waiting)], pauses: [pause], parts: [first, second])
        let full = RecordingDayCoverage.compute(record: record, part: first, sentToPositionBelow: 2, now: now)
        #expect(full.to == 20 && full.gaps.isEmpty, "the pause after its end, and the pending text, are part 2's")
        let open = RecordingDayCoverage.compute(record: record, part: second, sentToPositionBelow: 1, now: now)
        #expect(open.to == 30, "chunk 3's sentence is at part 2's position 1")
        let all = RecordingDayCoverage.compute(record: record, part: second, sentToPositionBelow: 3, now: now)
        #expect(all.to == 55, "chunk 5 is still waiting for transcription")
        #expect(all.gaps == [RecordingDayGap(from: 40, to: 45, reason: RecordingDayContract.Reason.interrupted)])
        #expect(!RecordingDayCoverage.isComplete(record: record, part: first, sentToPositionBelow: 2, coverage: full))

        // The day closed and all its text is on the hub: both parts are complete, the early one with the day.
        let closed = day(chunks, pauses: [pause], closedAt: 60, seqCount: 5, parts: [first, second])
        let done = RecordingDayCoverage.compute(record: closed, part: first, sentToPositionBelow: 2, now: now)
        #expect(RecordingDayCoverage.isComplete(record: closed, part: first, sentToPositionBelow: 2, coverage: done))
        let last = RecordingDayCoverage.compute(record: closed, part: second, sentToPositionBelow: 3, now: now)
        #expect(last.to == 60)
        #expect(RecordingDayCoverage.isComplete(record: closed, part: second, sentToPositionBelow: 3, coverage: last))
        // Part 2 isn't complete while part 1 still has to send one of its positions again.
        let behind = day(chunks, pauses: [pause], closedAt: 60, seqCount: 5, parts: [part(1, count: 2, next: 1, endedAt: 20), second])
        let waiting = RecordingDayCoverage.compute(record: behind, part: second, sentToPositionBelow: 3, now: now)
        #expect(waiting.to == 10)
        #expect(!RecordingDayCoverage.isComplete(record: behind, part: second, sentToPositionBelow: 3, coverage: waiting))
    }

    @Test func aFullPartTheHubLostTextOfIsCoveredOnlyUpToIt() {
        // Part 1 filled up at 20 s; reconciliation then found the hub holds only its first position.
        let record = day([chunk(0, at: 0, positions: [0]), chunk(1, at: 10, positions: [1]), chunk(2, at: 20, positions: [0], part: 2)])
        let first = part(1, count: 2, endedAt: 20)
        #expect(RecordingDayCoverage.compute(record: record, part: first, sentToPositionBelow: 1, now: now).to == 10,
                "chunk 1's sentence goes again with a later request")
        #expect(RecordingDayCoverage.compute(record: record, part: first, sentToPositionBelow: 0, now: now).to == 0)
        #expect(RecordingDayCoverage.compute(record: record, part: first, sentToPositionBelow: 2, now: now).to == 20)
        // Text a full part no longer takes (still waiting, or placed in the next part) never holds it back.
        let leftover = day([chunk(0, at: 0, positions: [0]), chunk(1, at: 10, state: .waiting), chunk(2, at: 20, positions: [0], part: 2)])
        #expect(RecordingDayCoverage.compute(record: leftover, part: part(1, count: 1, endedAt: 20), sentToPositionBelow: 1, now: now).to == 20)
    }

    @Test func overTheLimitTheClosestGapsOfOneReasonJoinAndErasedOnesNever() {
        // 501 paused gaps a second long, 10 s apart, except one interrupted and the closest pair of paused ones.
        var gaps = (0..<501).map { RecordingDayGap(from: Double($0) * 10, to: Double($0) * 10 + 1, reason: "paused") }
        gaps[300] = RecordingDayGap(from: 3000, to: 3001, reason: "interrupted")
        gaps[201] = RecordingDayGap(from: 2002, to: 2003, reason: "paused")
        let capped = RecordingDayCoverage.capped(gaps.shuffled(), to: RecordingDayCoverage.maxGaps)
        #expect(capped.count == 500)
        #expect(capped.contains(RecordingDayGap(from: 2000, to: 2003, reason: "paused")), "the closest pair of one reason")
        #expect(capped.contains(RecordingDayGap(from: 3000, to: 3001, reason: "interrupted")))
        #expect(zip(capped, capped.dropFirst()).allSatisfy { ($0.from, $0.to) <= ($1.from, $1.to) }, "in time order")
        #expect(RecordingDayCoverage.capped(Array(gaps.prefix(3)), to: 500) == Array(gaps.prefix(3)))

        // Erased ranges are kept exactly however close: the recap's withdrawal reads them.
        let erased = (0..<4).map { RecordingDayGap(from: Double($0) * 2, to: Double($0) * 2 + 1, reason: Reason.erased) }
        #expect(RecordingDayCoverage.capped(erased, to: 2) == erased)
        let mixed = erased + [RecordingDayGap(from: 20, to: 21, reason: "paused"), RecordingDayGap(from: 30, to: 31, reason: "paused")]
        let folded = RecordingDayCoverage.capped(mixed, to: 4)
        #expect(folded.filter { $0.reason == Reason.erased } == erased)
        #expect(folded.filter { $0.reason == "paused" } == [RecordingDayGap(from: 20, to: 31, reason: "paused")])
    }
}
