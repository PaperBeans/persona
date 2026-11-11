import SwiftUI

/// What ended a tool call, in words (`ToolOutcome`, #4303). A failure is red with its exit code. A call that was denied,
/// refused before it ran or interrupted is gray: nothing it ran failed (DESIGN_SYSTEM §4.8.1). A command that outran its
/// timeout went on in the background.
enum ToolOutcomeText {
    struct Headline: Equatable {
        var text: String
        var symbol: String
        var failed: Bool
    }

    /// The line over a result: nil for a call that finished, and for a failure the node gave no exit code (the result
    /// block says it failed in its own words).
    static func headline(_ outcome: ToolOutcome?) -> Headline? {
        switch outcome?.status {
        case "failed": outcome?.exitCode.map { Headline(text: String(localized: "Exit code \($0)"), symbol: Theme.Icon.errorFilled, failed: true) }
        case "denied": Headline(text: String(localized: "Denied"), symbol: Theme.Icon.denied, failed: false)
        case "blocked": Headline(text: String(localized: "Did not run"), symbol: Theme.Icon.didNotRun, failed: false)
        case "interrupted": Headline(text: String(localized: "Interrupted [Chat Layout]"), symbol: Theme.Icon.interrupt, failed: false)
        case "timed_out": Headline(text: String(localized: "Timed out"), symbol: Theme.Icon.time, failed: false)
        default: nil
        }
    }
}

/// The mark at the end of a tool row's head: a failure's red triangle, or in gray what stopped the call. Its words are
/// what VoiceOver reads.
struct ToolOutcomeMark: View {
    var call: ToolCall

    var body: some View {
        if call.failed {
            ThemedIcon(Theme.Icon.errorFilled).foregroundStyle(Theme.danger).font(Theme.Font.label)
                .accessibilityLabel(Text("Failed"))
        } else if call.outcome?.stopped == true, let head = ToolOutcomeText.headline(call.outcome) {
            ThemedIcon(head.symbol).foregroundStyle(.secondary).font(Theme.Font.label)
                .accessibilityLabel(Text(head.text))
        }
    }
}
