import Observation
import SwiftUI
import UserNotifications

/// A system entry point kept in AppModel. The action is claimed before it is sent; a change of appearance or a rebuilt window only redraws, never replays the write.
@Observable final class ExtensionSystemOpen: Identifiable {
    let id = UUID()
    let link: ExtensionSystemLink
    let action: ExtensionNotifyAction?
    let values: [String: String]
    private(set) var consumed = false

    init(link: ExtensionSystemLink, action: ExtensionNotifyAction? = nil, values: [String: String] = [:]) {
        self.link = link
        self.action = action
        self.values = values
    }

    func start(_ page: ExtensionViewModel) async -> ExtensionViewModel.Outcome {
        var outcome = ExtensionViewModel.Outcome()
        if !consumed, let action {
            consumed = true
            outcome = await page.perform(ExtensionAction(id: action.id), with: values)
        }
        if page.view == nil, !page.popped, !Task.isCancelled { await page.load() }
        return outcome
    }
}

extension AppModel {
    @discardableResult func openExtension(_ link: ExtensionSystemLink, action: ExtensionNotifyAction? = nil,
                                         values: [String: String] = [:]) -> Bool {
        guard api(link.group) != nil else {
            toast = Toast(String(localized: "The hub this extension belongs to is disconnected. Nothing was done."), style: .error)
            return false
        }
        extensionOpen = ExtensionSystemOpen(link: link, action: action, values: values)
        return true
    }

    /// Settings › Extensions › `id`: what an extension's `open_url` persona://settings/extensions/<id> opens (#5906). An
    /// extension page shown over everything closes first.
    func openExtensionSettings(_ id: String) {
        extensionOpen = nil
        settingsPath = NavigationPath([SettingsPage.plugins, SettingsPage.plugin(id)])
        #if os(macOS)
        MacShellState.shared.go(.settings, self)
        #else
        selectedTab = .settings
        #endif
    }

    /// Opens `url` when it is an extension's settings link (`ExtensionSettingsLink`); false for any other, which the
    /// caller opens as before.
    func openIfExtensionSettings(_ url: URL) -> Bool {
        guard let id = ExtensionSettingsLink.extensionID(url) else { return false }
        openExtensionSettings(id)
        return true
    }

    /// The subscription pages' Set Up with … (`persona://ask`, #5278; `ExtensionViewModel.opens` lets only them answer
    /// it): Settings' sheet closes and the text goes to the assistant's main conversation, which opens (`askPersona`; a
    /// sheet of the pages closes on its reveal).
    func openIfAsk(_ url: URL) -> Bool {
        guard let text = SubscriptionsUI.askText(url) else { return false }
        shellSettings = false
        Task { await askPersona(text) }
        return true
    }
}

/// Reuses the regular extension pages with their links, push/pop and invalidation refresh; no separate renderer for notifications.
private struct ExtensionSystemPresentation: ViewModifier {
    @Environment(AppModel.self) private var app

    func body(content: Content) -> some View {
        if let route = app.extensionOpen {
            NavigationStack {
                Group {
                    if let ui = route.link.ui {
                        ExtensionPageView(plugin: route.link.owner, ui: ui, title: String(localized: "Extensions"),
                                          state: route.link.state, group: route.link.group, initial: route)
                    } else {
                        EmptyStateView(symbol: Theme.Icon.plugins, title: String(localized: "No Page to Open"),
                                       message: String(localized: "This extension doesn't provide a full page."))
                    }
                }
                .toolbar {
                    ToolbarItem(placement: .confirmationAction) {
                        Button("Done [Extension System Presentation c0b3fb]") { app.extensionOpen = nil }.lineLimit(1)
                    }
                }
            }
            .environment(app)
        } else { content }
    }
}

extension View {
    func extensionSystemPresentation() -> some View { modifier(ExtensionSystemPresentation()) }
}

@MainActor enum ExtensionNotificationHandler {
    /// Keeps only short-lived callback dedup; another button on the same push is still a separate user action.
    private struct DeliveryKey: Hashable {
        var group: String
        var notification: String
        var action: String?
    }
    private static var handled: [DeliveryKey: Date] = [:]

    struct Deps {
        var group: (ExtensionNotificationEvent) -> String?
        var api: (String) -> APIClient?
        var open: (ExtensionSystemLink, ExtensionNotifyAction?, [String: String]) -> Void
        var report: (String, String, String) async -> Void
    }

    static func live(_ model: AppModel) -> Deps {
        Deps(group: { event in
            var mapping: [String: String] = [:]
            if let id = event.hubID, let group = model.registry.group(hubID: id) { mapping[id] = group }
            return event.group(hubGroups: mapping, groups: model.groupIDs)
        }, api: { model.api($0) }, open: { link, action, values in
            model.openExtension(link, action: action, values: values)
        }, report: { id, title, body in
            let content = UNMutableNotificationContent()
            content.title = title
            content.body = body
            try? await UNUserNotificationCenter.current().add(UNNotificationRequest(identifier: "extension-result-" + id,
                                                                                   content: content, trigger: nil))
        })
    }

    static func handle(_ event: ExtensionNotificationEvent, deps: Deps, deduplicate: Bool = true) async {
        guard !Task.isCancelled, !event.dismissed, event.valid else { return }
        guard let group = deps.group(event), let api = deps.api(group) else {
            await deps.report(event.notificationID, String(localized: "Extension Action Not Run"), String(localized: "Can't find the hub this notification came from. Reconnect, then do it in the app."))
            return
        }
        let key = DeliveryKey(group: group, notification: event.notificationID, action: event.action?.id)
        if deduplicate {
            handled = handled.filter { $0.value > Date.now.addingTimeInterval(-300) }
            guard handled[key] == nil else { return }
            handled[key] = .now
        }
        let route = ExtensionSystemLink(owner: event.payload.extension, ui: event.payload.ui,
                                        group: group, state: event.payload.state)
        if event.action == nil || event.action?.foreground == true {
            deps.open(route, event.action, event.values)
            return
        }
        guard let ui = event.payload.ui, let action = event.action else { return }
        do {
            let result = try await api.pluginUI(event.payload.extension, ui, context: .here(), state: event.payload.state,
                                                action: action.id, values: event.values.isEmpty ? nil : event.values)
            guard !Task.isCancelled else { return }
            // The background does not handle view, open_url, push or pop; only text feedback becomes a local notification.
            if let toast = result.toast, !toast.isEmpty { await deps.report(event.notificationID, String(localized: "Extension Action Done"), toast) }
        } catch {
            if !error.isCancellation {
                await deps.report(event.notificationID, String(localized: "Extension Action Didn't Finish"), error.localizedDescription)
            }
        }
    }
}
