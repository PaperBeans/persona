#if DEBUG
import SwiftUI

/// Only the explicit UI-test environment exposes the rendered chat's identity to XCUITest. Normal labels/values
/// stay unchanged, and this modifier is absent from Release. This is test data, not a user-facing session-id label.
struct UITestSessionProof: ViewModifier {
    var session: String

    @ViewBuilder func body(content: Content) -> some View {
        if ProcessInfo.processInfo.environment["PERSONA_UI_TEST_SESSION"] == "1" {
            content.accessibilityValue(session)
        } else {
            content
        }
    }
}
#endif
