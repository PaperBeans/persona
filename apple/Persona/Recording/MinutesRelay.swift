#if os(iOS)
import Foundation
import UIKit
import WatchConnectivity

/// The meetings' minutes on the Apple Watch (docs/apple/recording.md「Apple Watch」): the phone reads the hub's minutes of
/// its newest meeting recordings and relays a short version (`WatchMinutes`) in the WatchConnectivity application context;
/// the watch never talks to the hub for them. Read whenever the app comes to the front (and every few minutes while it
/// stays there, as long as some are still being written) and whenever a recording's page learns them.
@MainActor final class MinutesRelay {
    static let shared = MinutesRelay()

    private let library = RecordingLibrary.shared
    private var started = false
    private var known: [UUID: WatchMinutes.Meeting] = [:]
    /// The meetings whose minutes the hub holds back while its recording module is off (`detail.paused`, #15214).
    private var paused: Set<UUID> = []
    private var loop: Task<Void, Never>?
    /// The allowance the last relay went out with; nil until this run relayed anything.
    private var relayedRecording: RecordingAllowance?

    /// From `MeetingRecorder.start()` at launch.
    func start() {
        guard !started else { return }
        started = true
        NotificationCenter.default.addObserver(forName: UIApplication.didBecomeActiveNotification, object: nil, queue: .main) { @Sendable _ in
            Task { @MainActor in MinutesRelay.shared.follow() }
        }
        NotificationCenter.default.addObserver(forName: UIApplication.didEnterBackgroundNotification, object: nil, queue: .main) { @Sendable _ in
            Task { @MainActor in MinutesRelay.shared.loop?.cancel(); MinutesRelay.shared.loop = nil }
        }
    }

    /// The meeting recordings the watch hears about: the newest finished ones with text, a week back at most.
    func candidates(now: Date = .now) -> [RecordingEntry] {
        Array(library.entries
            .filter { $0.isMeeting && !$0.active && $0.transcribedAll && RecordingSync.uploadable($0)
                && $0.createdAt > now.addingTimeInterval(-7 * 86_400) }
            .sorted { $0.createdAt > $1.createdAt }
            .prefix(WatchMinutes.maxMeetings))
    }

    /// While the app is in front: reads now, then every three minutes as long as some are still being written.
    private func follow() {
        guard watchListens else { return }
        loop?.cancel()
        loop = Task { [weak self] in
            while !Task.isCancelled {
                guard let self, await self.refresh() else { return }
                try? await Task.sleep(for: .seconds(180))
            }
        }
    }

    /// Reads what isn't ready yet from the hub and relays the lot. True while some are still being written.
    @discardableResult
    func refresh() async -> Bool {
        guard watchListens, let api = AppModel.shared.api, !api.config.isDemo else { return false }
        let meetings = candidates()
        for entry in meetings where known[entry.id]?.state != .ready && RecordingSync.shared.status(entry) == .synced {
            guard let answer = try? await RecordingMinutesModel.fetch(entry.id, api) else { continue }
            known[entry.id] = Self.short(entry, minutes: answer.detail.minutes, status: answer.detail.meta?.state,
                                         paused: answer.detail.paused)
            // Held back while the hub's recording module is off (#15214): not worth asking again every three minutes.
            if answer.detail.paused?.isModuleOff == true { paused.insert(entry.id) } else { paused.remove(entry.id) }
        }
        push(meetings)
        return Self.stillWriting(meetings.map { ($0.id, known[$0.id]?.state) }, paused: paused,
                                 recording: RecordingModule.allowance)
    }

    /// Whether the follow keeps asking: while some minutes are still being written, and something can write them. With
    /// the hub's recording module off, or for the ones it says are paused for that, nothing does (#15214).
    nonisolated static func stillWriting(_ meetings: [(UUID, WatchMinutes.Meeting.State?)], paused: Set<UUID>,
                                         recording: RecordingAllowance) -> Bool {
        guard recording == .on else { return false }
        return meetings.contains { ($0.1 ?? .writing) == .writing && !paused.contains($0.0) }
    }

    /// What a recording's page just read: the watch hears it without a second request.
    func note(_ entry: RecordingEntry, state: RecordingMinutesModel.State) {
        guard entry.isMeeting, watchListens else { return }
        switch state {
        case .ready(let minutes): known[entry.id] = Self.short(entry, minutes: minutes, status: "ready")
        case .failed: known[entry.id] = Self.short(entry, minutes: nil, status: "failed")
        default: return
        }
        push(candidates())
    }

    /// The watch's short version of one meeting.
    nonisolated static func short(_ entry: RecordingEntry, minutes: RecordingMinutes?, status: String?,
                                  paused: RecordingPause? = nil) -> WatchMinutes.Meeting {
        let ready = minutes.map { !$0.isEmpty || status == "ready" } ?? false
        return WatchMinutes.meeting(id: entry.id, title: entry.displayTitle, date: entry.createdAt,
                                    minutes: ready ? minutes : nil, failed: status == "failed", paused: paused)
    }

    /// One meeting as the watch gets it (#15214): minutes still being written are marked paused while this phone's hub
    /// has its recording module off, whatever the hub said (a recording that never reached the hub has no answer of its
    /// own), so the watch reads Paused, not Writing, for ever. With the module on the meeting goes as it is.
    nonisolated static func relayed(_ meeting: WatchMinutes.Meeting, recording: RecordingAllowance) -> WatchMinutes.Meeting {
        guard recording == .off, meeting.state == .writing, meeting.paused == nil else { return meeting }
        var meeting = meeting
        meeting.paused = .moduleOff
        return meeting
    }

    /// The hub's recording module went on or off (`RecordingModule.tellWatch`, called at every hello): the meetings go
    /// again, marked or not. Only when the allowance differs from the one last relayed, and never before this run
    /// relayed anything: with nothing read yet every meeting would go as still being written, over minutes the watch
    /// already has. The next read relays them with the allowance of that moment anyway.
    func recordingChanged() {
        guard started, watchListens, let relayedRecording, relayedRecording != RecordingModule.allowance else { return }
        push(candidates())
    }

    private func push(_ meetings: [RecordingEntry]) {
        let recording = RecordingModule.allowance
        relayedRecording = recording
        let list = meetings.map { Self.relayed(known[$0.id] ?? Self.short($0, minutes: nil, status: nil), recording: recording) }
        WatchSync.shared.push(minutes: WatchMinutes(meetings: list))
    }

    private var watchListens: Bool {
        WCSession.isSupported() && WCSession.default.activationState == .activated && WCSession.default.isWatchAppInstalled
    }
}
#endif
