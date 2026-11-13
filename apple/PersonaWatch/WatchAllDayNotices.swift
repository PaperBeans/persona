import Foundation
import UserNotifications

/// All-day recording's notifications on the watch (`AllDayNotice`, docs/assistant/day-recap.md): paused and stopped
/// with Continue, the phone's request to start with Start, the phone's delete of the recording being made, and the
/// dead-man's switch. The watch posts them itself: the phone may be out of reach, and only the watch knows its
/// microphone stopped.
@MainActor
final class WatchAllDayNotices {
    /// Requests handed to the system and not confirmed yet: a WatchConnectivity wake stays until they are out.
    private(set) var inFlight = 0

    /// Our categories next to whatever else is registered; theirs are left as they are.
    nonisolated static func registerCategories() {
        UNUserNotificationCenter.current().getNotificationCategories { @Sendable existing in
            let resume = UNNotificationAction(identifier: AllDayNotice.continueAction, title: String(localized: "Continue"),
                                              options: [.foreground])
            let start = UNNotificationAction(identifier: AllDayNotice.startAction, title: String(localized: "Start"),
                                             options: [.foreground])
            let ours: Set<UNNotificationCategory> = [
                UNNotificationCategory(identifier: AllDayNotice.pausedCategory, actions: [resume], intentIdentifiers: [], options: []),
                UNNotificationCategory(identifier: AllDayNotice.startCategory, actions: [start], intentIdentifiers: [], options: []),
                UNNotificationCategory(identifier: AllDayNotice.deletedCategory, actions: [], intentIdentifiers: [], options: []),
            ]
            let others = existing.filter { !AllDayNotice.categories.contains($0.identifier) }
            UNUserNotificationCenter.current().setNotificationCategories(others.union(ours))
        }
    }

    /// Asks once, when All-Day Recording is first offered in front or started (`AllDayAlertPolicy.asksForNotifications`):
    /// a pause, a stop and the phone's start reach the wrist only as notifications while the screen is off. Returns
    /// whether notifications are off for the app now (the user said no), which the page says.
    @discardableResult func askOnce() async -> Bool {
        let center = UNUserNotificationCenter.current()
        let status = await center.notificationSettings().authorizationStatus
        guard status == .notDetermined else { return status == .denied }
        let granted = (try? await center.requestAuthorization(options: [.alert, .sound])) ?? false
        return !granted
    }

    /// Posts `notice` now, or `delay` seconds from now; one with the same request id is replaced. `group` is left once
    /// the system has taken it.
    func post(_ notice: AllDayAlertPolicy.Notice, after delay: TimeInterval? = nil, group: DispatchGroup) {
        let words = Self.words(notice)
        let content = UNMutableNotificationContent()
        content.title = words.title
        content.body = words.body
        content.sound = .default
        content.categoryIdentifier = notice.category
        // One thread for all of all-day recording's notices.
        content.threadIdentifier = AllDayNotice.pausedCategory
        // Not time-sensitive: the watch's App ID has no such entitlement.
        content.interruptionLevel = .active
        // The phone's start request keeps when the phone sent it: an answer after a newer command, or a late one, is
        // the user's own start (`AllDayNotice.askedByPhone`).
        if case .start(let sentAt) = notice { content.userInfo = [AllDayNotice.sentAtKey: sentAt] }
        let trigger = delay.map { UNTimeIntervalNotificationTrigger(timeInterval: $0, repeats: false) }
        inFlight += 1
        group.enter()
        UNUserNotificationCenter.current().add(UNNotificationRequest(identifier: notice.request, content: content, trigger: trigger)) { @Sendable _ in
            group.leave()
            Task { @MainActor in self.inFlight -= 1 }
        }
    }

    /// Arms (or moves on) the dead-man's switch: a stopped notice `AllDayAlertPolicy.deadManSeconds` ahead, which fires
    /// only if the app dies and stops renewing it.
    func armDeadMan(group: DispatchGroup) {
        post(.stopped(.crashed), after: AllDayAlertPolicy.deadManSeconds, group: group)
    }

    func disarmDeadMan() {
        UNUserNotificationCenter.current().removePendingNotificationRequests(withIdentifiers: [AllDayNotice.stoppedRequest])
    }

    /// Takes back what was said (it's answered now): the delivered notices (a deletion's too, once the user starts
    /// again), and a pending start or pause notice. The dead-man's switch is `disarmDeadMan`'s.
    func withdraw() {
        let center = UNUserNotificationCenter.current()
        center.removeDeliveredNotifications(withIdentifiers: AllDayNotice.requests)
        center.removePendingNotificationRequests(withIdentifiers: [AllDayNotice.pausedRequest, AllDayNotice.startRequest])
    }

    /// The phone took its start request back (its switch turned off) before the notice was answered.
    func withdrawStart() {
        let center = UNUserNotificationCenter.current()
        center.removeDeliveredNotifications(withIdentifiers: [AllDayNotice.startRequest])
        center.removePendingNotificationRequests(withIdentifiers: [AllDayNotice.startRequest])
    }

    /// Whether a notice with this request id is in Notification Center now.
    func isDelivered(_ request: String) async -> Bool {
        await withCheckedContinuation { continuation in
            UNUserNotificationCenter.current().getDeliveredNotifications { @Sendable notes in
                continuation.resume(returning: notes.contains { $0.request.identifier == request })
            }
        }
    }

    /// Whether a notice with this request id is still scheduled (the dead-man's switch, before it fires).
    func isPending(_ request: String) async -> Bool {
        await withCheckedContinuation { continuation in
            UNUserNotificationCenter.current().getPendingNotificationRequests { @Sendable requests in
                continuation.resume(returning: requests.contains { $0.identifier == request })
            }
        }
    }

    /// Title and body: what happened, what is safe, the one next step.
    private static func words(_ notice: AllDayAlertPolicy.Notice) -> (title: String, body: String) {
        switch notice {
        case .paused(let why): (String(localized: "Recording Paused"), body(why))
        case .stopped(let why): (String(localized: "Recording Stopped"), body(why))
        case .start: (String(localized: "All-Day Recording"),
                      String(localized: "Your iPhone asked to start it. Tap Start to record on this watch."))
        case .deleted: (String(localized: "Recording Stopped"),
                        String(localized: "Today's all-day recording was deleted on your iPhone."))
        }
    }

    private static func body(_ why: RecordingDayHalt) -> String {
        switch why {
        case .interrupted: String(localized: "A call or another app paused all-day recording. Tap Continue to pick it up again.")
        case .resumeFailed: String(localized: "All-day recording couldn't pick up again by itself. Tap Continue to record.")
        case .microphoneChanged: String(localized: "The microphone changed, so all-day recording paused. Tap Continue to pick it up again.")
        case .audioReset: String(localized: "The system audio service reset. What was recorded is kept; tap Continue to record again.")
        case .lowBattery: String(localized: "The watch battery is too low. What was recorded is kept; charge the watch, then tap Continue.")
        case .lowStorage: String(localized: "The watch is almost out of storage. What was recorded is kept; free up space, then tap Continue.")
        case .writeFailed: String(localized: "Part of the recording couldn't be saved. What was recorded before is kept; tap Continue to record again.")
        case .crashed: String(localized: "Persona stopped while recording all day. What was recorded is kept; tap Continue to record again.")
        }
    }
}
