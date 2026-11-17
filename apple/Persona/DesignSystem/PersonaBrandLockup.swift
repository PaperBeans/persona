import SwiftUI

/// Persona's mark wherever the app signs a picture (#9468): the assistant's face, its name and "(Persona)" after it,
/// quiet. One component for every place, so the exported picture's footer and the screenshot banner look the same.
/// `.mark` is the small signature at the foot of an exported picture; `.banner` the large one a screenshot shows where
/// a chat's top bar is (#9508), and `.slim` the same in a folded header's slim bar.
struct PersonaBrandLockup: View {
    enum Size {
        case mark, banner, slim
    }

    @Environment(AppModel.self) private var model
    var size: Size = .mark
    /// The name shown: the assistant's (`AppModel.assistantShownName`) unless given.
    var name: String?

    var body: some View {
        let shown = name ?? model.assistantShownName
        switch size {
        case .mark:
            HStack(spacing: Theme.Space.xs) {
                PersonaAvatar(size: Theme.Size.badge)
                Text(verbatim: shown)
                    .font(Theme.Font.labelStrong)
                    .foregroundStyle(.secondary)
                // Its name already says Persona: not "Persona (Persona)".
                if Self.showsProduct(after: shown) {
                    Text("(Persona) [Brand Lockup]")
                        .font(Theme.Font.label)
                        .foregroundStyle(.tertiary)
                }
            }
            .lineLimit(1)
            .accessibilityElement(children: .combine)
        case .banner, .slim:
            // As large as the bar it stands in for: the name first to shrink and cut, "(Persona)" always whole.
            let banner = size == .banner
            HStack(spacing: Theme.Space.s) {
                PersonaAvatar(size: banner ? Theme.Size.avatarM : Theme.Size.wellXS)
                Text(verbatim: shown)
                    .font(banner ? Theme.Font.display : Theme.Font.heading)
                    .foregroundStyle(.primary)
                    .truncationMode(.tail)
                if Self.showsProduct(after: shown) {
                    Text("(Persona) [Brand Lockup]")
                        .font(banner ? Theme.Font.displayPlain : Theme.Font.body)
                        .foregroundStyle(.secondary)
                        .layoutPriority(1)
                }
            }
            .lineLimit(1)
            .minimumScaleFactor(0.8)
            // A bar's text (6.2): it grows with the text size up to xxxLarge, never past the bar.
            .dynamicTypeSize(...Theme.Shell.topBarLargestTypeSize)
        }
    }

    /// The product's own name, never translated.
    static let product = "Persona"

    /// "(Persona)" follows the name unless the name already is Persona's.
    static func showsProduct(after name: String) -> Bool {
        name.trimmingCharacters(in: .whitespaces).caseInsensitiveCompare(product) != .orderedSame
    }
}
