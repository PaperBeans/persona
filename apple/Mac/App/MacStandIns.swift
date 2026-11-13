import Foundation
import Observation

// Mac versions of the iOS app's system integrations that have no macOS counterpart (docs/apple/mac.md「Approach」).
// Same names and calls as on iOS, so AppModel and the pages stay as they are.

/// Live Activities / the Dynamic Island do not exist on a Mac. The hub's fleet state is kept here for the
/// menu bar (docs/apple/mac.md: Dynamic Island → menu bar); nothing is started or ended.
@Observable final class LiveActivityController {
    static let shared = LiveActivityController()

    /// The hub's latest fleet summary (what the island would show).
    private(set) var fleet: FleetState?
    /// The device's notification settings, as the hub keeps them.
    private(set) var prefs: NotifyPrefs?
    /// Interactions settled from this Mac, hidden until the hub's next fleet push agrees.
    private var resolved: Set<String> = []

    func start() {}
    func settings(_ prefs: NotifyPrefs) { self.prefs = prefs }
    func apply(_ hubState: FleetState) {
        fleet = hubState
        resolved = []
    }
    func resolvedLocally(_ interactionID: String) { resolved.insert(interactionID) }
    func isResolvedLocally(_ interactionID: String) -> Bool { resolved.contains(interactionID) }
    func endAll() { fleet = nil }
    func showSample(_ phase: String, details: Bool, island spec: String?) {}
}

/// There is no Apple Watch paired to a Mac: the pairing and summary pushes go nowhere.
final class WatchSync: NSObject {
    static let shared = WatchSync()

    var onConfig: ((ServerConfig) -> Void)?
    var onSummary: ((WatchSummary) -> Void)?
    var onActivated: (() -> Void)?
    var configProvider: (() -> ServerConfig?)?
    var configsProvider: (() -> WatchConfigs?)?

    var watchAppInstalled: Bool { false }

    func activate() {}
    func push(_ all: WatchConfigs) {}
    func push(language: String) {}
    func push(persona: String) {}

    func push(summary: @autoclosure () -> WatchSummary) {}

    // All-day recording's phone side (Shared/WatchSync.swift): no watch sends chunks or events to a Mac, so no hook is
    // ever called and nothing is sent.
    nonisolated(unsafe) var onDayChunk: (@Sendable (_ frame: Data, _ reply: @escaping @Sendable (Data) -> Void) -> Void)?
    nonisolated(unsafe) var onDayFile: (@Sendable (_ header: RecordingDayHeader, _ file: URL) -> Bool)?
    nonisolated(unsafe) var onDayEvent: (@Sendable (_ envelope: RecordingDayEventEnvelope) -> RecordingDayAck?)?
    var onDayErased: ((RecordingDayErased) -> Void)?
    var onDayDeleted: ((RecordingDayDeleted) -> Void)?
    var onDayWatchState: ((RecordingDayWatchState) -> Void)?

    func pushRecordingDay(consent: Bool, hub: Bool, keepDays: Int) {}
    func sendDayCommand(_ command: String) {}
    nonisolated func sendDayAck(_ ack: RecordingDayAck) {}
    func sendDayErase(_ request: RecordingDayEraseRequest) {}
    func sendDayDelete(_ delete: RecordingDayDelete) {}
}
