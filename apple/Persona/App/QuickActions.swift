#if os(iOS)
import UIKit

/// The home screen icon's long-press menu (#3199): the last conversation opened here (#2191, `RecentChatsStore`), then
/// Record a Meeting, New Chat and Inbox; the system shows four at most, so Agents is not among them (the tab bar has it).
/// They only open a page; with no hub paired the app stays on its welcome page. Record a Meeting starts recording too
/// (docs/apple/recording.md「Meeting recordings」), with or without a hub: the text syncs once there is one.
enum QuickAction: String, CaseIterable {
    case meetingRecording = "persona.meeting-recording"
    case newConversation = "persona.new-conversation"
    case inbox = "persona.inbox"
    /// A recently opened conversation: its `SessionKey.path` in the item's user info.
    static let recentType = "persona.recent"

    var item: UIApplicationShortcutItem {
        let (title, symbol): (String, String) = switch self {
        case .meetingRecording: (String(localized: "Record a Meeting"), Theme.Icon.recording)
        case .newConversation: (String(localized: "New Chat [Quick Actions ea4b29]"), Theme.Icon.compose)
        case .inbox: (String(localized: "Inbox"), Theme.Icon.inbox)
        }
        return UIApplicationShortcutItem(type: rawValue, localizedTitle: title, localizedSubtitle: nil,
                                         icon: UIApplicationShortcutIcon(systemImageName: symbol))
    }

    /// The menu now: at launch, when a conversation is opened and when the app goes to the background (the titles as
    /// they are then). A conversation the app no longer has is left out.
    @MainActor static func publish() {
        let model = AppModel.shared
        let recent = RecentChatsStore.shared.latest(RecentChats.homeScreen) { model.session($0) != nil }.compactMap { key -> UIApplicationShortcutItem? in
            guard let s = model.session(key) else { return nil }
            return UIApplicationShortcutItem(type: recentType, localizedTitle: s.displayTitle, localizedSubtitle: recentSubtitle(s, key: key),
                                             icon: UIApplicationShortcutIcon(systemImageName: Theme.Icon.recent), userInfo: ["key": key.path as NSString])
        }
        UIApplication.shared.shortcutItems = recent + allCases.map(\.item)
    }

    /// Where a recent conversation is: "Persona" for one of Persona's, else its computer and folder.
    @MainActor private static func recentSubtitle(_ s: Session, key: SessionKey) -> String {
        if s.assistant { return "Persona" }
        let machine = AppModel.shared.machine(key.machineRef)?.name ?? key.machineID
        return [machine, s.folderName].filter { !$0.isEmpty }.joined(separator: " · ")
    }

    /// The item the menu was tapped with: true when it was one of these.
    @MainActor @discardableResult
    static func perform(_ item: UIApplicationShortcutItem) -> Bool {
        let model = AppModel.shared
        if item.type == recentType, let path = item.userInfo?["key"] as? String, let key = SessionKey(path: path) {
            model.open(key) // an Agent's chat or a Persona conversation, as a notification opens it
            return true
        }
        guard let action = QuickAction(rawValue: item.type) else { return false }
        model.shellSettings = false
        switch action {
        case .meetingRecording:
            Task { await MeetingRecorder.shared.startMeeting(inForeground: true) }
        case .newConversation:
            model.selectedTab = .assistant
            // As the drawer's New Chat: the shell takes Assistant back to its root (a pushed inbox included), the old tabs only
            // need the conversation gone.
            if let shell = model.tabRouter as? ShellState {
                shell.show(.newConversation)
                shell.drawer.close()
            } else {
                model.assistantCurrent = nil
            }
        case .inbox: model.selectedTab = .mail
        }
        return true
    }
}

/// The scene's delegate only for the menu: a tap while the app runs or is in the background comes here; one that
/// launched it comes with the scene's connection options. SwiftUI still owns the window.
final class QuickActionSceneDelegate: NSObject, UIWindowSceneDelegate {
    func scene(_ scene: UIScene, willConnectTo session: UISceneSession, options connectionOptions: UIScene.ConnectionOptions) {
        if let item = connectionOptions.shortcutItem { Task { @MainActor in QuickAction.perform(item) } }
    }

    func windowScene(_ windowScene: UIWindowScene, performActionFor shortcutItem: UIApplicationShortcutItem,
                     completionHandler: @escaping (Bool) -> Void) {
        Task { @MainActor in completionHandler(QuickAction.perform(shortcutItem)) }
    }
}
#endif
