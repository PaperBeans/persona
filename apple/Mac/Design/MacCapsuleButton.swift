import SwiftUI

/// The Mac's `themedButton()`: a capsule drawn here. The system's glass button filled itself with the accent whenever
/// the window was the active one, and a quick answer's 「推荐」 tag, in the AI's purple, disappeared into the blue
/// (2026-11-02 user: the background color covers the text, it's unreadable); in a window behind it was gray. Prominent: white words on the tint, as
/// Send on the inbox's draft card; the others: the tint's words on a light wash of it, as Edit beside it and the chips
/// are, over the style's surface (glass), so the round ones floating over a chat or a photo (Jump to Latest, Previous image) keep a
/// backdrop. Follows the tint (Interrupt sets red) and the control size.
struct MacCapsuleButton: ButtonStyle {
    var prominent: Bool
    /// Pending decisions can request a larger target without changing existing utility buttons.
    var minimumHeight: CGFloat? = nil
    @Environment(\.isEnabled) private var enabled
    @Environment(\.controlSize) private var size

    func makeBody(configuration: Configuration) -> some View {
        let label = configuration.label
            .padding(.horizontal, horizontal)
            .padding(.vertical, vertical)
            .scaledFrame(minHeight: minimumHeight)
            .foregroundStyle(prominent ? AnyShapeStyle(Theme.macOnTint) : AnyShapeStyle(.tint))
            .contentShape(.capsule)
        Group {
            if prominent {
                label.background(.tint, in: .capsule)
            } else {
                label.background(.tint.opacity(Theme.macButtonWash), in: .capsule).surface(interactive: true, in: .capsule)
            }
        }
        .opacity(enabled ? (configuration.isPressed ? Theme.macButtonPressed : 1) : Theme.macButtonDisabled)
    }

    private var horizontal: CGFloat {
        switch size {
        case .mini, .small: Theme.Space.s
        case .large, .extraLarge: Theme.Space.l
        default: Theme.Space.m
        }
    }

    private var vertical: CGFloat {
        switch size {
        case .mini, .small: Theme.Space.xxs
        case .large, .extraLarge: Theme.Space.s
        default: Theme.Space.xs
        }
    }
}

extension Theme {
    /// How much of the tint a plain button's capsule takes (as a tinted chip's fill).
    static let macButtonWash = 0.12
    /// A button held down, and one that cannot be pressed (an empty composer's send must not look ready).
    static let macButtonPressed = 0.7
    static let macButtonDisabled = 0.4
}
