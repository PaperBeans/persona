#if DEBUG
import Foundation

/// `-showAllDaySample recording|paused|interrupted|stopped|off|consent|deleted`: all-day recording in that state with no
/// microphone, 3 h 25 min since it last started, 4 chunks waiting for the iPhone (screenshots), as a phone whose user
/// agreed shows it (`recordingDay` 1): the home card and the Record page's button. `consent`: off, before the user
/// agreed on the iPhone (`recordingDay` 0: the watch points there); `deleted`: off, just after the phone deleted the
/// recording being made. Pair it with `-recording.phoneDayHub NO` (the "needs a hub update" line), `-openWatchItem
/// allday` (its page) or `-openWatchItem allday-complication` (every face).
enum WatchAllDaySamples {
    static let day = UUID(uuidString: "00000000-0000-0000-0000-000000000006")!

    /// A sample by name: the state it shows, the phone's `recordingDay`, and whether the phone just deleted it.
    private struct Sample {
        let state: AllDaySnapshot.State
        let phoneDay: Int
        let deleted: Bool

        init?(_ name: String) {
            switch name {
            case "consent": self.init(state: .off, phoneDay: 0)
            case "deleted": self.init(state: .off, deleted: true)
            default:
                guard let state = AllDaySnapshot.State(rawValue: name) else { return nil }
                self.init(state: state)
            }
        }

        private init(state: AllDaySnapshot.State, phoneDay: Int = RecordingDayContract.version, deleted: Bool = false) {
            self.state = state
            self.phoneDay = phoneDay
            self.deleted = deleted
        }
    }

    @MainActor static func installIfAsked() {
        guard let raw = UserDefaults.standard.string(forKey: "showAllDaySample"), let sample = Sample(raw) else { return }
        // First: the capture's launch recovery runs now, not over the sample.
        let capture = RecordingCapture.shared
        let elapsed: Double = 3 * 3600 + 25 * 60
        switch sample.state {
        case .recording, .paused, .interrupted:
            capture.showSample(day, elapsed: elapsed, paused: sample.state == .paused, interrupted: sample.state == .interrupted,
                               allDay: true)
        case .stopped, .off:
            break
        }
        WatchAllDay.shared.showSample(sample.state, since: .now.addingTimeInterval(-elapsed), waiting: 4,
                                      phoneDay: sample.phoneDay, deleted: sample.deleted)
    }
}
#endif
