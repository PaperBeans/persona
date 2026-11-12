import SwiftUI

nonisolated extension ComponentSpec {
    /// Lark: flat white cards on light gray in every color, hairline borders, bordered buttons, like the Lark app.
    static let lark: ComponentSpec = {
        let stroke = Color(light: 0xDEE0E3, dark: 0x373A40)
        return ComponentSpec(
            title: String(localized: "Lark [Inbox Event Views 7e4419]"), subtitle: String(localized: "Like Lark"),
            surface: .hairline, buttons: .bordered, fontDesign: .default, previewCorner: 10, components: .persona,
            canvas: .neutral(page: Color(light: 0xF5F6F7, dark: 0x141414), card: Color(light: 0xFFFFFF, dark: 0x1F1F1F)),
            cardStroke: stroke,
            // The Lark gateway's --message-incoming and its card chrome.
            larkOtherBubble: StyleSpec.larkGrayBubble, larkCard: Color(light: 0xFFFFFF, dark: 0x1E1F23),
            mac: larkMac, colors: .lark)
    }()

    /// Feishu's desktop window (2026-11-12 user: the Mac app was black and white beside it): a blue-gray navigation
    /// column whose selected entry is a white block with its icon in the accent, a lighter column beside the list,
    /// selected rows a pale wash of the accent, and people without a picture on colored discs. Measured from the
    /// Feishu desktop app; the accent's icon is ≥ 3:1 on every selected shade, the white letter ≥ 3.6:1 on every hue.
    private static let larkMac = StyleSpec.MacChrome(
        sidebar: Color(light: 0xE4EAF6, dark: 0x1C1D21),
        sidebarSelected: Color(light: 0xFFFFFF, dark: 0x2C2E34),
        sideColumn: Color(light: 0xEDF1F7, dark: 0x18191C),
        selectionTint: 0.12,
        hover: Color(light: 0x1F2329, dark: 0xFFFFFF).opacity(0.06),
        monogramHues: [0x3370FF, 0x4E5BE6, 0x7F3BF5, 0x0E8F8F, 0x2E9A3F, 0xD9620B, 0xE0403B, 0xD6337A, 0x1F87C7]
            .map { Color(hex: $0) })
}

nonisolated extension ColorSpec {
    /// Lark's blue (Lark's own); Lark's pages keep the Lark gateway's --message-outgoing bubble.
    static let lark = ColorSpec(
        title: String(localized: "Lark Blue"), subtitle: String(localized: "Lark [Inbox Event Views 7e4419]"),
        accent: Pair(light: 0x3370FF, dark: 0x4C88FF),
        bubble: Color(light: 0xD1E3FF, dark: 0x133063), bubbleText: .primary, deepensBubble: false, larksOwn: true)
}
