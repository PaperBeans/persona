import SwiftUI

// The colors of a reply's Markdown (`MarkdownUI.Theme.persona`, laid out again for selecting by `ReplyText`). A file of
// their own, away from Theme.swift (the reply-block PRs edit it too). Shared/, so the watch compiles them; it draws none
// of it.

nonisolated extension Theme {
    enum Reply {
        /// The quieter Markdown headings (H4–H6, `ReplyMetrics.heading4`, #3863): ≥ 4.5:1 on every style's page and card
        /// and on Persona's reply box in light and dark (`StyleSpecTests`), where `.secondary` is about 3.4:1 on white.
        /// Apple's systemGray2 pair the other way round: its dark value in light mode, its light value in dark.
        static let secondaryInk = Color(light: secondaryInkHex.light, dark: secondaryInkHex.dark)
        /// The values above, for the contrast test.
        static let secondaryInkHex: (light: UInt32, dark: UInt32) = (0x636366, 0xAEAEB2)
    }
}
