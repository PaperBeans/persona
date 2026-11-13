#if os(iOS)
import AppIntents
import Foundation

/// Ticks a to-do off from the To-dos widget (medium and large). It runs in the app (the widget extension never
/// reaches the hub): the app marks it done in the widget's snapshot at once, tells the hub, and on a refusal marks it
/// failed, so the widget says it did not go through and a tap tries again
/// (docs/apple/home-widgets.md「Ticking off a to-do」). The extension builds only the type.
struct CompleteTodoIntent: AppIntent {
    nonisolated static let title: LocalizedStringResource = "Complete To-Do [Home Widgets]"
    nonisolated static let isDiscoverable = false
    nonisolated static var authenticationPolicy: IntentAuthenticationPolicy { .requiresAuthentication }

    @Parameter(title: "To-Do [Home Widgets]") var todoID: String

    nonisolated init() {}
    nonisolated init(todoID: String) { self.todoID = todoID }

    nonisolated func perform() async throws -> some IntentResult {
        #if !WIDGET_EXTENSION
        await HomeWidgetActions.complete(todoID: todoID)
        #endif
        return .result()
    }
}
#endif
