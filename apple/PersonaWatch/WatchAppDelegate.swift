import UserNotifications
import WatchKit

/// The watch app's delegate: the notification delegate for all-day recording's notices (it must be set before launch
/// finishes, or a tap on a notice that launched the app is lost). It answers only for our categories and leaves every
/// other notification as it was before it existed: not shown while the app is in front, a tap just opens the app.
final class WatchAppDelegate: NSObject, WKApplicationDelegate, UNUserNotificationCenterDelegate {
    func applicationDidFinishLaunching() {
        UNUserNotificationCenter.current().delegate = self
        WatchAllDay.shared.install()
    }

    // Completion-handler forms on purpose: the async ones return on a background executor (docs/lessons/ios.md).

    /// Looked at, the page and the wrist tap already said it: the notice goes to Notification Center without a banner.
    /// Frontmost but inactive (wrist down while it records) no haptic played, so the banner and its sound say it
    /// (`AllDayNotice.presentsBanner`), judged on the main thread as `WatchAllDay` judges the haptic.
    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification,
                                            withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void) {
        guard AllDayNotice.categories.contains(notification.request.content.categoryIdentifier) else {
            completionHandler([])
            return
        }
        nonisolated(unsafe) let done = completionHandler
        DispatchQueue.main.async {
            MainActor.assumeIsolated {
                let active = WKApplication.shared().applicationState == .active
                done(AllDayNotice.presentsBanner(appActive: active) ? [.banner, .sound, .list] : [.list])
            }
        }
    }

    /// Continue or Start brings the app to the front, where the microphone can open; a tap on the notice opens the page.
    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse,
                                            withCompletionHandler completionHandler: @escaping () -> Void) {
        let category = response.notification.request.content.categoryIdentifier
        guard AllDayNotice.categories.contains(category),
              response.actionIdentifier != UNNotificationDismissActionIdentifier else {
            completionHandler()
            return
        }
        let link = AllDayNotice.link(answering: response.actionIdentifier)
        // The phone's request counts only while no newer command (its Stop) came and it is recent.
        let sentAt = response.notification.request.content.userInfo[AllDayNotice.sentAtKey] as? Double
        let byPhone = AllDayNotice.askedByPhone(answering: response.actionIdentifier, sentAt: sentAt,
                                                newest: UserDefaults.standard.double(forKey: WatchDayCommandOrder.lastKey),
                                                now: .now)
        nonisolated(unsafe) let done = completionHandler
        DispatchQueue.main.async {
            MainActor.assumeIsolated {
                WatchAllDay.shared.open(link, byPhone: byPhone)
                done()
            }
        }
    }
}
