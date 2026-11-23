import SwiftUI

nonisolated extension Theme {
    /// Intake colors are shared by the app and future widgets. Their hues stay apart from Apple's Activity rings.
    enum Health {
        static let waterHex: (light: UInt32, dark: UInt32) = (0x2457D6, 0x719BFF)
        static let proteinHex: (light: UInt32, dark: UInt32) = (0x9A5900, 0xF6B650)
        static let water = Color(light: waterHex.light, dark: waterHex.dark)
        static let protein = Color(light: proteinHex.light, dark: proteinHex.dark)
        // Apple HIG Activity ring label colors, always on black, never themed or dimmed.
        static let activityBackground = Color.black
        static let move = Color(hex: 0xFA114F)
        static let exercise = Color(hex: 0xA6FF00)
        static let stand = Color(hex: 0x00FFF6)
        static let activitySide = Theme.Size.box160
        static let ringMargin = Theme.Space.s
    }
}
