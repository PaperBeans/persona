import SwiftUI

/// "Plan awaiting approval · answer in the chat ›" (#10465; DESIGN_SYSTEM 4.8, "To-dos"; proposal 3.7, "Drawn
/// once per screen"): the one line a door to a request shrinks to while the request's home is on the same screen (the
/// to-do's conversation pane, a pushed session, the pinned header), so a screen never shows a request twice. No buttons:
/// the whole line goes to the home. The request in the needs-you ink, the way there in the secondary.
struct AskPointer: View {
    /// What is asked, in the request's title words (DESIGN_SYSTEM 5.6) or the ask's own.
    var ask: String
    var go: () -> Void

    var body: some View {
        Button(action: go) {
            HStack(alignment: .firstTextBaseline, spacing: Theme.Space.xs) {
                Text(Self.words(ask))
                    .lineLimit(2)
                ThemedIcon(Theme.Icon.forward)
                    .font(Theme.Font.labelHeavy)
                    .foregroundStyle(.tertiary)
                    .accessibilityHidden(true)
            }
            .font(Theme.Font.secondary)
            .tapTargetOutset()
        }
        .buttonStyle(.plain)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(String(localized: "\(ask), answer in the chat [To-dos Answers]"))
        .accessibilityAddTraits(.isButton)
        .accessibilityIdentifier("ask.pointer")
    }

    /// The request in the needs-you ink, then the way there: one paragraph, wrapping as one line of text.
    private static func words(_ ask: String) -> AttributedString {
        var said = AttributedString(ask)
        said.foregroundColor = StatusPalette.needsYou.text
        var rest = AttributedString(TodoTimeWords.separator + String(localized: "answer in the chat [To-dos Answers]"))
        rest.foregroundColor = .secondary
        return said + rest
    }
}
