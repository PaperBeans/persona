#if os(iOS)
import Foundation

/// Where a meeting recording is, from what the phone already knows (docs/design/proposals/meeting-recording.md section 5).
/// A pure function of plain inputs: it adds no state to the recording code, and the live sheet, the wrap-up and their
/// tests all read the same table. The wrap-up's steps come from the phase (`steps`).
nonisolated enum RecordingPhase: Equatable, Sendable {
    /// Nothing records and no wrap-up is held: the start hero.
    case idle
    /// Start was tapped and recording has not begun: the calendar read, the microphone. `cancellable` once the capture is
    /// `starting` (the calendar read before it has nothing to cancel).
    case preparing(cancellable: Bool)
    case recording
    /// Paused by the user, or by the system (a call, Siri).
    case paused(byCall: Bool)
    /// Stopped, and the recording is not all in yet (a watch recording: its segments are still arriving).
    case ending
    case transcribing
    /// The text goes to the hub. `waitingForHub`: it cannot now (not paired, demo mode, offline), so nothing spins.
    case syncing(waitingForHub: Bool)
    case writingMinutes
    /// Everything that can happen has. `hasMinutes`: the minutes are there to open.
    case done(hasMinutes: Bool)
    case failed(Failure)

    /// The step that failed, why, and the one thing the page can do about it (nil: nothing exists to do, the reason only).
    nonisolated struct Failure: Equatable, Sendable {
        var step: Step.Kind
        var reason: String
        var action: Action?
    }

    nonisolated enum Action: Equatable, Sendable {
        case resumeTranscribing
        case tryAgainMinutes
    }

    nonisolated enum Capture: Equatable, Sendable { case idle, starting, recording, paused }

    /// Where the recording's minutes are (`RecordingMinutesModel.State`); nil is "not asked yet".
    nonisolated enum Minutes: Equatable, Sendable {
        case absent, waitingForText, waitingForSync, writing, ready, failed, unsupported
    }

    /// Where the upload is (`RecordingSync.Status`).
    nonisolated enum Sync: Equatable, Sendable {
        case waiting, pending, uploading, synced, rejected, expired
    }

    /// What the recording held for the wrap-up says about itself.
    nonisolated struct Held: Equatable, Sendable {
        var isMeeting: Bool
        /// The recording is still `active` on the entry.
        var active: Bool
        /// Every segment has arrived (`RecordingEntry.receivedAll`).
        var receivedAll: Bool
        /// It ended with no audio at all (`expectedSegments == 0`).
        var noAudio: Bool
        var transcribedAll: Bool
        /// The transcriber is on it now.
        var transcribing: Bool
        var transcriptionError: String?
        var sync: Sync
        /// The hub can be reached now (paired, not the demo). Without it a pending upload waits for a hub.
        var hubAvailable: Bool
        var minutes: Minutes?
        /// What to say when the hub refused the text, and when it could not write the minutes.
        var syncRejectedReason = ""
        var minutesFailedReason = ""
    }

    nonisolated struct Inputs: Equatable, Sendable {
        /// The Record tap is being worked on (a local flag): covers the calendar read.
        var startRequested = false
        var capture: Capture = .idle
        var interrupted = false
        /// `RecordingCapture.error`: why a recording ended or could not start.
        var captureError: String?
        /// The recording the sheet holds for its wrap-up, once capture ended. nil: none is held.
        var held: Held?
    }

    /// The table of section 5, as a function. The first rule that applies wins.
    static func derive(_ i: Inputs) -> RecordingPhase {
        switch i.capture {
        case .recording: return .recording
        case .paused: return .paused(byCall: i.interrupted)
        case .starting: return .preparing(cancellable: true)
        case .idle: break
        }
        if i.startRequested { return .preparing(cancellable: false) }
        guard let held = i.held else { return .idle }
        if let reason = i.captureError, !reason.isEmpty {
            return .failed(Failure(step: .saved, reason: reason, action: nil))
        }
        if held.active || (!held.receivedAll && !held.noAudio) { return .ending }
        if held.noAudio { return .done(hasMinutes: false) }
        if let reason = held.transcriptionError, !held.transcribing {
            return .failed(Failure(step: .transcribed, reason: reason, action: .resumeTranscribing))
        }
        if held.transcribing || !held.transcribedAll { return .transcribing }
        switch held.sync {
        case .rejected:
            return .failed(Failure(step: .synced, reason: held.syncRejectedReason, action: nil))
        case .pending, .uploading:
            return .syncing(waitingForHub: !held.hubAvailable)
        case .expired:
            return .done(hasMinutes: false)
        case .waiting, .synced:
            break
        }
        guard held.isMeeting else { return .done(hasMinutes: false) }
        // Synced (or nothing to send): a meeting's minutes decide what is left. Not asked yet counts as writing: the first
        // answer takes a moment, and `done` must not flash before it.
        switch held.minutes {
        case nil:
            return held.sync == .waiting ? .done(hasMinutes: false) : .writingMinutes
        case .waitingForText: return .transcribing
        case .waitingForSync: return .syncing(waitingForHub: !held.hubAvailable)
        case .writing: return .writingMinutes
        case .ready: return .done(hasMinutes: true)
        case .failed: return .failed(Failure(step: .writing, reason: held.minutesFailedReason, action: .tryAgainMinutes))
        case .absent, .unsupported: return .done(hasMinutes: false)
        }
    }

    /// The live sheet shows its live layout in these.
    var isLive: Bool {
        switch self {
        case .recording, .paused: true
        default: false
        }
    }

    /// The wrap-up shows in these.
    var isWrapUp: Bool {
        switch self {
        case .ending, .transcribing, .syncing, .writingMinutes, .done, .failed: true
        default: false
        }
    }

    /// Nothing more happens by itself: the wrap-up can stop asking.
    var isSettled: Bool {
        switch self {
        case .done, .failed: true
        case .syncing(let waiting): waiting
        default: false
        }
    }
}

// MARK: What the live sheet does when capture changes

/// The live sheet's reaction to `RecordingCapture.state` changing, and the error that belongs to a stop. Pure, so the
/// transitions (a stop while starting, paused straight to idle, a new recording while the wrap-up is shown) are tested.
nonisolated enum RecordingSheetMove: Equatable, Sendable {
    case none
    /// A recording is on: the sheet holds its id and shows the live view. `announce`: it just began.
    case hold(UUID, announce: Bool)
    /// Capture ended while the sheet held a recording: the wrap-up replaces the live view.
    case wrapUp
    /// Capture ended and the sheet holds nothing (a start that was cancelled or refused): it closes.
    case dismiss

    static func move(from old: RecordingPhase.Capture, to new: RecordingPhase.Capture, recordingID: UUID?, held: UUID?) -> RecordingSheetMove {
        switch new {
        case .recording, .paused:
            guard let recordingID, held != recordingID else { return .none }
            return .hold(recordingID, announce: new == .recording)
        case .idle:
            if held != nil { return old == .recording || old == .paused ? .wrapUp : .none }
            return .dismiss
        case .starting:
            return .none
        }
    }

    /// The reason a recording ended badly, when the stop itself caused it (a failed write, low storage, an audio reset).
    /// `RecordingCapture.error` also holds what happened while it ran and could be undone (the microphone changed, so it
    /// paused): that one is `whileLive`, the value last seen while capture ran, and a recording saved fine after it is
    /// not a failure.
    static func stopError(now: String?, whileLive: String?) -> String? {
        guard let now, !now.isEmpty, now != whileLive else { return nil }
        return now
    }
}

// MARK: The wrap-up's steps

nonisolated extension RecordingPhase {
    /// One row of the wrap-up: Saved, Transcribed, Synced to Knowledge, Writing minutes, Minutes ready.
    nonisolated struct Step: Equatable, Identifiable, Sendable {
        nonisolated enum Kind: Int, CaseIterable, Sendable { case saved, transcribed, synced, writing, minutes }
        nonisolated enum State: Equatable, Sendable {
            /// Not started.
            case waiting
            case current
            /// Held up for the hub (no spinner).
            case waitingForHub
            case done
            case failed(String)
        }
        var kind: Kind
        var state: State
        var id: Int { kind.rawValue }
    }

    /// The steps for `phase`. A step before the current one is done, the current one runs, the rest wait; a failed step
    /// names its reason. The minutes' two steps are for a meeting, and only when it ends with minutes or is still on its
    /// way to them.
    static func steps(for phase: RecordingPhase, isMeeting: Bool) -> [Step] {
        var kinds = Step.Kind.allCases
        if !isMeeting { kinds.removeAll { $0 == .writing || $0 == .minutes } }
        if case .done(let hasMinutes) = phase, !hasMinutes { kinds.removeAll { $0 == .writing || $0 == .minutes } }
        let current: Int // the first step not done yet; past the end: all done
        var failed: (Step.Kind, String)?
        var hub = false
        switch phase {
        case .ending: current = Step.Kind.saved.rawValue
        case .transcribing: current = Step.Kind.transcribed.rawValue
        case .syncing(let waiting): current = Step.Kind.synced.rawValue; hub = waiting
        case .writingMinutes: current = Step.Kind.writing.rawValue
        case .failed(let f): current = f.step.rawValue; failed = (f.step, f.reason)
        case .done: current = Step.Kind.allCases.count
        case .idle, .preparing, .recording, .paused: current = Step.Kind.saved.rawValue
        }
        return kinds.map { kind in
            let state: Step.State
            if let failed, failed.0 == kind { state = .failed(failed.1) }
            else if kind.rawValue < current { state = .done }
            else if kind.rawValue == current { state = hub ? .waitingForHub : .current }
            else { state = .waiting }
            return Step(kind: kind, state: state)
        }
    }
}
#endif
