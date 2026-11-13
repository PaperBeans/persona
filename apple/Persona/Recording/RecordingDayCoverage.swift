import Foundation

/// The phone's view of a day for the hub (docs/assistant/day-recap.md「The recording」): `coverage` in every create,
/// repair and append, sent whole each time. `to` is how far, in seconds since the day's `started_at`, every chunk the
/// phone holds is transcribed AND on the hub after the request that carries it: the hub reads the day only up to
/// there, so a `to` past text it hasn't received yet would make it note an hour as finished without that text. `gaps`
/// is the time with no text and why (`RecordingDayGap.gaps(in:since:now:)`, the same the day's page shows). Pure:
/// computed from the record as it is, for the request about to go.
nonisolated struct RecordingDayCoverage: Codable, Sendable, Equatable {
    var to: Double
    var gaps: [RecordingDayGap]
}

/// What the hub holds of a day's text once a request for one of its parts is stored: that part's positions below what
/// the request leaves there, every other part's below its confirmed `next`. A part the hub keeps no text of any more
/// holds nothing back.
nonisolated struct RecordingDayHeld: Sendable, Equatable {
    /// Per part number, the count of positions on the hub.
    var below: [Int: Int]

    init(record: RecordingDayRecord, part: Int, sent: Int) {
        var below: [Int: Int] = [:]
        for other in record.parts { below[other.part] = other.expired || other.deleted ? Int.max : other.next }
        below[part] = sent
        self.below = below
    }

    func holds(_ position: RecordingDayPosition) -> Bool { position.pos < (below[position.part] ?? 0) }
}

nonisolated extension RecordingDayCoverage {
    /// Gaps sent at most; past this the closest ones of one reason are joined, as the hub would join them.
    static let maxGaps = 500
    /// A pause the watch hasn't ended yet runs to now; its end moves in steps this long, so the coverage changes (and
    /// an append goes for it) at most this often while the microphone stays closed.
    static let openGapStep: TimeInterval = 300

    /// Coverage of `part` for a request after which the hub holds this part's positions below `sentToPositionBelow`.
    /// - The part that takes the day's text: `to` walks the seqs in order and stops at the first chunk not covered
    ///   (`covered`). A seq the phone doesn't hold stops it until the first later chunk the phone holds arrived
    ///   `gapWaitSeconds` ago (its persisted `receivedAt`, so a relaunch doesn't restart the wait), and the gap stays
    ///   `pending` until the chunk comes; a seq reported lost or erased never stops it. Once the day is closed and every
    ///   seq below its count is covered, `to` reaches the close: no chunk comes after it.
    /// - A part closed early (`413 day_full`) takes no new text, so only its own positions hold `to` back: it reaches
    ///   the part's end once the request leaves every one of them on the hub, and stops before the first chunk holding
    ///   one it doesn't. Its `pending` gaps moved to the next part, and gaps after its end are not its own.
    static func compute(record: RecordingDayRecord, part: RecordingDayPart, sentToPositionBelow sent: Int, now: Date) -> RecordingDayCoverage {
        let base = record.startedAt
        let held = RecordingDayHeld(record: record, part: part.part, sent: sent)
        let stepped = Date(timeIntervalSince1970: (now.timeIntervalSince1970 / openGapStep).rounded(.down) * openGapStep)
        var gaps = RecordingDayGap.gaps(in: record, since: base, now: stepped)
        let to: Double
        if let end = part.endedAt {
            to = closedReach(record: record, part: part.part, held: held, end: end)
            // Its text is all from before its end, so later gaps say nothing about it.
            let limit = end.timeIntervalSince(base)
            gaps.removeAll { $0.reason == RecordingDayGap.Reason.pending || $0.from >= limit }
        } else {
            to = reach(record: record, held: held, now: now)
        }
        return RecordingDayCoverage(to: max(0, to), gaps: capped(gaps, to: maxGaps))
    }

    /// Whether a request with this coverage can say `complete` (docs/assistant/day-recap.md「The recording」): the
    /// watch's `closed` is applied, no gap is `pending`, and every seq below its count is reported lost or erased, or
    /// held with its text final and on the hub after the request (a silent chunk counts; one the phone could not
    /// transcribe does not: the hub's cutoff recap covers that day). The same for every part, so a part closed early
    /// becomes complete together with the day, never before.
    static func isComplete(record: RecordingDayRecord, part: RecordingDayPart, sentToPositionBelow sent: Int,
                           coverage: RecordingDayCoverage) -> Bool {
        guard record.closedAt != nil, record.seqCount != nil, record.missingSeqs.isEmpty, sent >= part.count,
              !coverage.gaps.contains(where: { $0.reason == RecordingDayGap.Reason.pending }) else { return false }
        let held = RecordingDayHeld(record: record, part: part.part, sent: sent)
        return record.chunks.values.allSatisfy { $0.hasSettledText && isOnHub($0, held) }
    }

    /// When a missing seq's wait (`gapWaitSeconds` from the first later chunk's arrival) ends after `now`, the soonest:
    /// `to` may move past it then, so the upload looks again. nil when nothing is waiting.
    static func nextGapExpiry(record: RecordingDayRecord, now: Date) -> Date? {
        let held = record.chunks.keys.sorted()
        guard let last = held.last else { return nil }
        var soonest: Date?
        var index = 0
        var seq = 0
        while seq < last {
            guard !record.isAccounted(seq) else {
                seq += 1
                continue
            }
            // The first held chunk after this missing run.
            while index < held.count, held[index] <= seq { index += 1 }
            guard index < held.count, let later = record.chunks[held[index]] else { break }
            let expiry = later.receivedAt.addingTimeInterval(RecordingDayContract.gapWaitSeconds)
            if expiry > now { soonest = min(soonest ?? expiry, expiry) }
            seq = held[index]
        }
        return soonest
    }

    /// How far the covered chunks reach for the part that takes the day's text (`compute`).
    private static func reach(record: RecordingDayRecord, held: RecordingDayHeld, now: Date) -> Double {
        let base = record.startedAt
        let keys = record.chunks.keys.sorted()
        let end = max(record.knownSeqEnd, (keys.last ?? -1) + 1)
        var to = 0.0
        var index = 0
        for seq in 0..<end {
            if let chunk = record.chunks[seq] {
                guard covered(chunk, held) else { return to }
                to = max(to, chunk.endedAt.timeIntervalSince(base))
            } else if !record.lost.contains(seq), !record.erasedSeqs.contains(seq) {
                // The watch still holds it: wait for it, until the chunk after it has been here `gapWaitSeconds`.
                while index < keys.count, keys[index] <= seq { index += 1 }
                guard index < keys.count, let later = record.chunks[keys[index]],
                      now.timeIntervalSince(later.receivedAt) >= RecordingDayContract.gapWaitSeconds else { return to }
            }
        }
        // Closed, and every seq below its count settled: nothing comes after the last chunk.
        if let closed = record.closedAt, record.seqCount != nil { to = max(to, closed.timeIntervalSince(base)) }
        return to
    }

    /// How far a part closed early reaches: its end, unless a chunk holds one of its positions the hub won't have.
    private static func closedReach(record: RecordingDayRecord, part: Int, held: RecordingDayHeld, end: Date) -> Double {
        let base = record.startedAt
        let limit = end.timeIntervalSince(base)
        var to = 0.0
        for chunk in record.chunks.values.sorted(by: { $0.seq < $1.seq }) {
            if (chunk.positions ?? []).contains(where: { $0.part == part && !held.holds($0) }) { return min(to, limit) }
            to = max(to, chunk.endedAt.timeIntervalSince(base))
        }
        return limit
    }

    /// Whether a chunk holds nothing back: no text belongs to it (erased, lost), its time is silence or an untranscribed
    /// gap, or its text is placed and on the hub after the request.
    private static func covered(_ chunk: RecordingDayChunk, _ held: RecordingDayHeld) -> Bool {
        if chunk.isErasedOrLost { return true }
        switch chunk.state {
        case .skipped, .failed: return true
        case .waiting: return false
        case .transcribed: return isOnHub(chunk, held)
        }
    }

    /// Every sentence of the chunk has a position the hub holds after the request.
    private static func isOnHub(_ chunk: RecordingDayChunk, _ held: RecordingDayHeld) -> Bool {
        !chunk.hasUnplacedText && (chunk.positions ?? []).allSatisfy(held.holds)
    }

    /// At most `limit` gaps: while there are more, the two neighbours of one reason with the least time between them
    /// become one (the shortest interruptions are folded away first). Erased ranges are never joined: the recap's
    /// withdrawal reads them exactly. Gaps the joins can't bring under the limit are sent as they are; the hub mends.
    static func capped(_ gaps: [RecordingDayGap], to limit: Int) -> [RecordingDayGap] {
        guard gaps.count > max(1, limit) else { return gaps }
        let sorted = gaps.sorted { ($0.from, $0.to, $0.reason) < ($1.from, $1.to, $1.reason) }
        var byReason: [String: [Int]] = [:]
        for (index, gap) in sorted.enumerated() where gap.reason != RecordingDayGap.Reason.erased {
            byReason[gap.reason, default: []].append(index)
        }
        var pairs: [(space: Double, first: Int, second: Int)] = []
        for indices in byReason.values {
            for (first, second) in zip(indices, indices.dropFirst()) {
                pairs.append((sorted[second].from - sorted[first].to, first, second))
            }
        }
        pairs.sort { ($0.space, $0.first) < ($1.space, $1.first) }
        // Each joined gap points at the one before it of its reason; following the pointers (always to a lower index)
        // finds the gap a run of joins starts with.
        var joinedTo = Array(sorted.indices)
        for pair in pairs.prefix(sorted.count - max(1, limit)) { joinedTo[pair.second] = pair.first }
        var root = Array(sorted.indices)
        var merged: [Int: RecordingDayGap] = [:]
        for index in sorted.indices {
            root[index] = joinedTo[index] == index ? index : root[joinedTo[index]]
            let gap = sorted[index]
            if var into = merged[root[index]] {
                into.from = min(into.from, gap.from)
                into.to = max(into.to, gap.to)
                merged[root[index]] = into
            } else {
                merged[root[index]] = gap
            }
        }
        return merged.values.sorted { ($0.from, $0.to, $0.reason) < ($1.from, $1.to, $1.reason) }
    }
}
