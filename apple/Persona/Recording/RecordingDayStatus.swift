import Foundation

/// The words the Recordings page and a day's page use for all-day recording (docs/assistant/day-recap.md): the All-Day
/// Recording switch's line, how far a day has got, one state word per day, and its gaps by reason. Pure: the views
/// render these values, and tests assert the enums rather than localized text. Foundation only, so the Mac build (which
/// compiles `Persona/` but never shows a day) compiles it too.
nonisolated enum RecordingDayStatus {
    // MARK: The switch

    /// A command the phone sent the watch whose answer (the watch's new state) hasn't come yet.
    nonisolated struct Pending: Equatable, Hashable, Sendable {
        /// true: start (or continue) all-day recording; false: stop it for today.
        var start: Bool
        var at: Date
    }

    /// How long the switch shows a command as on its way. The watch app can't open the microphone from the background,
    /// so a start waits for a tap there (its notification), and a command to a watch app that isn't running waits for
    /// its next launch.
    static let answerWait: TimeInterval = 120
    /// How long after that the line still says the watch hasn't answered, before it goes back to the watch's state.
    static let noAnswerShown: TimeInterval = 600

    /// The switch row's line.
    nonisolated enum Line: Equatable, Sendable {
        /// Not recording all day (or the watch never said).
        case off
        /// Asked the watch to start; waiting for a tap there.
        case starting
        /// Asked the watch to stop.
        case stopping
        /// Asked, and the watch didn't answer within `answerWait`.
        case noAnswer
        /// The watch records; `since`: when it began this state.
        case recording(since: Date?)
        /// The user paused it on the watch.
        case paused
        /// A call, Siri or another app took the microphone: the watch waits for a tap on Continue.
        case interrupted
        /// It stopped by itself (battery, storage, a crash): the watch waits for a tap on Continue.
        case stopped
    }

    /// The command still waiting: nil once the watch's state shows it done (on for a start, off for a stop). Settling
    /// only on a matching state, never on any change: the watch publishes its state again whenever its count of chunks
    /// waiting for the phone changes, which says nothing about the command.
    static func settle(_ pending: Pending?, watch: RecordingDayWatchState?) -> Pending? {
        guard let pending else { return nil }
        return (watch?.isOn ?? false) == pending.start ? nil : pending
    }

    /// What the switch row says now.
    static func line(watch: RecordingDayWatchState?, pending: Pending?, now: Date) -> Line {
        if let pending = settle(pending, watch: watch) {
            let waited = now.timeIntervalSince(pending.at)
            if waited < answerWait { return pending.start ? .starting : .stopping }
            if waited < answerWait + noAnswerShown { return .noAnswer }
        }
        switch watch?.state {
        case .recording: return .recording(since: watch?.since)
        case .paused: return .paused
        case .interrupted: return .interrupted
        case .stopped: return .stopped
        case .off, nil: return .off
        }
    }

    /// Whether the switch is on: the command on its way, else the watch's state (recording, paused or interrupted).
    static func isOn(watch: RecordingDayWatchState?, pending: Pending?, now: Date) -> Bool {
        if let pending = settle(pending, watch: watch), now.timeIntervalSince(pending.at) < answerWait { return pending.start }
        return watch?.isOn ?? false
    }

    /// When the line next changes by itself (a command's wait running out), for the view to look again then.
    static func nextChange(watch: RecordingDayWatchState?, pending: Pending?, now: Date) -> Date? {
        guard let pending = settle(pending, watch: watch) else { return nil }
        return [pending.at.addingTimeInterval(answerWait), pending.at.addingTimeInterval(answerWait + noAnswerShown)]
            .first { $0 > now }
    }

    // MARK: A day

    /// The one word for where a day is, for its row, the Today row and its page.
    nonisolated enum Word: Equatable, Sendable {
        case noAudio
        case noSpeech
        case waitingForWatch
        case waitingToTranscribe
        case waitingToSync
        case synced
        /// The hub doesn't take day recordings (yet): the text stays here until it does.
        case keptOnPhone
        /// The upload stopped for good (`RecordingDayRecord.problem`).
        case notAccepted
        case pastRetention
        case recapWriting
        case recapReady
        case recapFailed
    }

    /// How a word is drawn; a word always says the state itself, never the color alone.
    nonisolated enum Tone: Equatable, Sendable {
        case secondary, accent, positive, caution, danger
    }

    /// A day's word: an upload that stopped first, then the recap once the hub has one under way, then what still
    /// happens on the devices (audio the watch holds, chunks to transcribe), then where the text is with the hub.
    static func word(_ day: RecordingDaySummary) -> Word {
        if day.sync == .problem { return .notAccepted }
        switch day.recapState {
        case "ready"?: return .recapReady
        case "failed"?: return .recapFailed
        case "pending"?: return .recapWriting
        default: break
        }
        let watchHolds = day.gaps.contains { $0.reason == RecordingDayGap.Reason.pending }
        if day.chunks == 0 { return watchHolds ? .waitingForWatch : .noAudio }
        if day.waiting > 0 { return .waitingToTranscribe }
        if watchHolds { return .waitingForWatch }
        switch day.sync {
        case .notOffered, .unsupported: return .keptOnPhone
        case .expired: return .pastRetention
        case .pending: return .waitingToSync
        case .synced, .problem: break
        }
        // Every chunk with audio was quieter than speech: nothing to read.
        return day.skipped == day.chunks ? .noSpeech : .synced
    }

    /// How much of a day is recorded, and how far it is transcribed.
    nonisolated enum Progress: Equatable, Sendable {
        case recorded(seconds: Double)
        case transcribed(seconds: Double, to: Date)
    }

    /// nil before any audio arrived (the word says why).
    static func progress(_ day: RecordingDaySummary) -> Progress? {
        guard day.recordedSeconds > 0 else { return nil }
        guard let to = day.transcribedTo else { return .recorded(seconds: day.recordedSeconds) }
        return .transcribed(seconds: day.recordedSeconds, to: to)
    }

    /// From when the watch began the day recording to the end of its last audio the phone holds, nil before any audio.
    static func span(_ day: RecordingDaySummary) -> Range<Date>? {
        guard let last = day.lastAt else { return nil }
        return day.firstAt..<max(day.firstAt, last)
    }

    /// Whether the day's page offers "Erase Last 10 Minutes": only for today's recording while it is open
    /// (docs/assistant/day-recap.md「Erasing」). A closed day ended at its midnight or was deleted, so its last ten
    /// minutes are not the ten minutes just said. `now`: the board's (`RecordingDayBoard.now`), which moves on at
    /// midnight, so the button goes then even when nothing else about the day changes.
    static func offersErase(_ day: RecordingDaySummary, now: Date) -> Bool {
        day.closedAt == nil && isToday(day: day.day, timeZone: day.timeZone, now: now)
    }

    /// Whether the Today section says the hub doesn't write day recaps yet: only once that is about something (a day
    /// exists, or the user agreed to all-day recording or turned it on). A watch user who never used the mode keeps the
    /// page as it was, without a lasting caution about a feature they haven't used.
    static func showsHubUpdate(offered: Bool, sync: RecordingDaySyncState?, hasDay: Bool, agreed: Bool, on: Bool) -> Bool {
        guard hasDay || agreed || on else { return false }
        return !offered || sync == .unsupported
    }

    /// What the day's page says when its upload stopped for good (`RecordingDaySummary.problem`, the code the phone or
    /// the hub gave): why, in a sentence, since the code itself means nothing to the user. A code without words of its
    /// own (a `400`'s) gets the plain refusal.
    static func problemText(_ code: String?) -> String {
        switch code {
        case "too_large": String(localized: "This day's text was too large for Knowledge to take, so it stays on this iPhone.")
        case "day_full": String(localized: "Knowledge holds as much of this day as it can, so the rest stays on this iPhone.")
        case "not_day", "kind_changed":
            String(localized: "Knowledge keeps another kind of recording in this day's place, so its text stays on this iPhone.")
        default: String(localized: "Knowledge didn't accept this day's text, so it stays on this iPhone.")
        }
    }

    /// Why there is no text, grouped as the day's page lists them.
    nonisolated enum GapReason: Int, CaseIterable, Comparable, Sendable {
        case paused, interrupted, stopped, pending, lost, erased, untranscribed, other

        init(_ reason: String) {
            switch reason {
            case RecordingDayGap.Reason.paused: self = .paused
            case RecordingDayGap.Reason.interrupted: self = .interrupted
            case RecordingDayGap.Reason.stopped: self = .stopped
            case RecordingDayGap.Reason.pending: self = .pending
            case RecordingDayGap.Reason.lost: self = .lost
            case RecordingDayGap.Reason.erased: self = .erased
            case RecordingDayGap.Reason.untranscribed: self = .untranscribed
            default: self = .other
            }
        }

        static func < (a: Self, b: Self) -> Bool { a.rawValue < b.rawValue }
    }

    nonisolated struct GapTotal: Equatable, Sendable {
        var reason: GapReason
        var seconds: Double
    }

    /// Each reason's total time, in `GapReason` order; reasons without time left out.
    static func gapTotals(_ gaps: [RecordingDayGap]) -> [GapTotal] {
        var totals: [GapReason: Double] = [:]
        for gap in gaps where gap.to > gap.from {
            totals[GapReason(gap.reason), default: 0] += gap.to - gap.from
        }
        return totals.filter { $0.value > 0 }.sorted { $0.key < $1.key }.map { GapTotal(reason: $0.key, seconds: $0.value) }
    }

    // MARK: Today

    /// Whether `day` (`YYYY-MM-DD` in `timeZone`) is today there.
    static func isToday(day: String?, timeZone: String?, now: Date) -> Bool {
        guard let day else { return false }
        return RecordingDayCalendar.day(of: now, in: zone(timeZone)) == day
    }

    /// Today's day recording among the library's rows: the one the watch records if it is today's, else the one begun
    /// last. The Today section shows it and Recent Recordings leaves it out, so it is never listed twice (C30). The same
    /// rule as `RecordingDaySummaries.today`, from the library's rows, which exist before any summary is read.
    static func todayID(_ entries: [RecordingEntry], watching: UUID?, now: Date) -> UUID? {
        var today: [RecordingEntry] = []
        var dayInZone: [String: String] = [:]
        for entry in entries where entry.isDay {
            let key = entry.timeZone ?? ""
            let current = dayInZone[key] ?? RecordingDayCalendar.day(of: now, in: zone(entry.timeZone))
            dayInZone[key] = current
            if entry.day == current { today.append(entry) }
        }
        if let watching, today.contains(where: { $0.id == watching }) { return watching }
        return today.max { ($0.createdAt, $0.id.uuidString) < ($1.createdAt, $1.id.uuidString) }?.id
    }

    /// The first midnight after `date` in any of `zones` (each day rolls over at its own), else in the phone's: when
    /// "today" next changes for one of them.
    static func nextMidnight(after date: Date, zones: some Sequence<TimeZone>) -> Date {
        zones.map { RecordingDayCalendar.nextMidnight(after: date, in: $0) }.min()
            ?? RecordingDayCalendar.nextMidnight(after: date, in: .autoupdatingCurrent)
    }

    /// A day's name, as `RecordingEntry.displayTitle` writes an untitled day: its date in its own time zone.
    static func title(day: String, timeZone: String) -> String {
        let dayZone = Self.zone(timeZone)
        guard let start = RecordingDayCalendar.start(of: day, in: dayZone) else { return day }
        let date = start.formatted(Date.FormatStyle(date: .abbreviated, time: .omitted, timeZone: dayZone))
        return String(localized: "Day · \(date)")
    }

    /// The zone a day is a date in; the phone's own for a missing or unknown identifier.
    static func zone(_ identifier: String?) -> TimeZone {
        identifier.flatMap(TimeZone.init(identifier:)) ?? .autoupdatingCurrent
    }

    // MARK: Formatting

    /// The locale every clock time of a day is written in: the interface language's (docs/protocol/i18n.md), which is
    /// fixed for the launch.
    private static let clockLocale = Locale.interface

    /// Every clock time of all-day recording, wherever it shows: the switch's "since", the Today row's "transcribed to",
    /// a day's span on its row and page, the recap's ranges and time chips, the transcript's hours and lines. One style,
    /// in the interface locale and in the zone of the recording the time belongs to, so the same moment reads the same
    /// everywhere (a span's "09:12" beside a line's "9:12" read as two different styles). `seconds`: the transcript's
    /// lines only.
    static func clock(_ date: Date, in zone: TimeZone, seconds: Bool = false) -> String {
        date.formatted(Date.FormatStyle(date: .omitted, time: seconds ? .standard : .shortened, locale: clockLocale, timeZone: zone))
    }

    /// From one clock time to another, each end written by `clock` (9:12–19:07), as the kept segments of a recording
    /// write theirs.
    static func spanText(_ span: Range<Date>, in zone: TimeZone) -> String {
        let from = clock(span.lowerBound, in: zone), to = clock(span.upperBound, in: zone)
        // A span within one minute reads as that minute, not "9:41–9:41".
        return from == to ? from : "\(from)–\(to)"
    }

    /// When something was written: its clock time today, else its date and time ("Written 21:00" / "Written Nov 11,
    /// 21:00"), in the interface locale; a relative "now" reads oddly beside a recap of a whole day.
    static func writtenText(_ date: Date, now: Date = .now, in zone: TimeZone = .autoupdatingCurrent) -> String {
        var calendar = Calendar(identifier: .gregorian); calendar.timeZone = zone
        if calendar.isDate(date, inSameDayAs: now) { return clock(date, in: zone) }
        return date.formatted(Date.FormatStyle(date: .abbreviated, time: .shortened, locale: clockLocale, timeZone: zone))
    }

    /// A length of time as the system writes it ("2 hr, 14 min"); seconds only below a minute.
    static func duration(_ seconds: Double) -> String {
        let value = Duration.seconds(max(0, seconds.isFinite ? seconds : 0))
        let units: Set<Duration.UnitsFormatStyle.Unit> = seconds < 60 ? [.seconds] : [.hours, .minutes]
        return value.formatted(.units(allowed: units, width: .abbreviated))
    }
}

// MARK: - Text

nonisolated extension RecordingDayStatus.Line {
    /// The switch row's detail. `zone`: the zone of the day recording the watch records, the phone's when it isn't known
    /// (the watch's state carries no zone).
    func text(in zone: TimeZone) -> String {
        switch self {
        case .off: String(localized: "Records the whole day on your Apple Watch")
        case .starting: String(localized: "Tap Start on your Apple Watch to begin")
        case .stopping: String(localized: "Stopping on your Apple Watch…")
        case .noAnswer: String(localized: "Your Apple Watch hasn't answered. Open Persona on it.")
        case .recording(let since?): String(localized: "Recording all day since \(RecordingDayStatus.clock(since, in: zone))")
        case .recording(nil): String(localized: "Recording all day")
        case .paused: String(localized: "Paused on your Apple Watch")
        case .interrupted: String(localized: "Paused — tap Continue on your Apple Watch")
        case .stopped: String(localized: "Stopped — tap Continue on your Apple Watch")
        }
    }
}

nonisolated extension RecordingDayStatus.Word {
    var text: String {
        switch self {
        case .noAudio: String(localized: "No audio recorded")
        case .noSpeech: String(localized: "No speech detected")
        case .waitingForWatch: String(localized: "Waiting for Apple Watch")
        case .waitingToTranscribe: String(localized: "Waiting to transcribe")
        case .waitingToSync: String(localized: "Waiting to sync")
        case .synced: String(localized: "Synced to Knowledge")
        case .keptOnPhone: String(localized: "Kept on this iPhone")
        case .notAccepted: String(localized: "Not accepted by Knowledge")
        case .pastRetention: String(localized: "Past retention")
        case .recapWriting: String(localized: "Writing the recap")
        case .recapReady: String(localized: "Recap ready")
        case .recapFailed: String(localized: "Recap failed")
        }
    }

    var tone: RecordingDayStatus.Tone {
        switch self {
        case .noAudio, .noSpeech, .waitingForWatch, .waitingToTranscribe, .waitingToSync, .keptOnPhone, .pastRetention: .secondary
        case .recapWriting: .accent
        case .synced, .recapReady: .positive
        case .notAccepted, .recapFailed: .danger
        }
    }
}

nonisolated extension RecordingDayStatus.Progress {
    /// "2 hr, 14 min recorded · transcribed to 14:32", the time in the day's zone.
    func text(in zone: TimeZone) -> String {
        switch self {
        case .recorded(let seconds):
            String(localized: "\(RecordingDayStatus.duration(seconds)) recorded")
        case .transcribed(let seconds, let to):
            String(localized: "\(RecordingDayStatus.duration(seconds)) recorded · transcribed to \(RecordingDayStatus.clock(to, in: zone))")
        }
    }
}

nonisolated extension RecordingDayStatus.GapReason {
    var text: String {
        switch self {
        case .paused: String(localized: "Paused")
        case .interrupted: String(localized: "Interrupted")
        case .stopped: String(localized: "Stopped")
        case .pending: String(localized: "Waiting for Apple Watch")
        case .lost: String(localized: "Lost on Apple Watch")
        case .erased: String(localized: "Erased")
        case .untranscribed: String(localized: "Not transcribed")
        case .other: String(localized: "Other")
        }
    }
}
