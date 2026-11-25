import SwiftUI
import PersonaWire

/// The disk usage map's tokens (#14576, docs/server/machine-usage-map.md "The map"; docs/design/DESIGN_SYSTEM.md "Usage
/// treemap"): the seven category fills of the Kind tint, the four steps of the Last Used tint (coldest strongest), the
/// one ink drawn on every fill (at least 4.5:1 on each in both modes, `StyleSpecTests`), and the layout numbers ported
/// from the sibling product's nested treemap. Data colors, like `UsageColor` and `MetricColor`: the same in every style
/// and theme color. The web's token sheet carries the same values.
extension Theme {
    nonisolated enum UsageMap {
        // MARK: Kind tint

        static let apps = PersonaPalette.Pair(light: 0xA9C8F5, dark: 0x234B82)
        static let personal = PersonaPalette.Pair(light: 0xA6DBB3, dark: 0x1F5C35)
        static let appdata = PersonaPalette.Pair(light: 0xCDB9F2, dark: 0x4A3685)
        static let cache = PersonaPalette.Pair(light: 0xF6C78F, dark: 0x7A4411)
        static let system = PersonaPalette.Pair(light: 0xC5CAD3, dark: 0x404753)
        static let free = PersonaPalette.Pair(light: 0xEEF0F3, dark: 0x1C1F23)
        static let other = PersonaPalette.Pair(light: 0xDADADF, dark: 0x35353A)

        static func pair(_ c: StorageUsageCategory) -> PersonaPalette.Pair {
            switch c {
            case .apps: apps
            case .personal: personal
            case .appdata: appdata
            case .cache: cache
            case .system: system
            case .free: free
            case .other: other
            }
        }

        static func fill(_ c: StorageUsageCategory) -> Color { pair(c).color }

        // MARK: Last Used tint

        /// From changed lately to untouched for over a year; the coldest is the strongest.
        enum Age: Int, CaseIterable, Sendable, Comparable {
            case recent, months, halfYear, year

            static func < (a: Self, b: Self) -> Bool { a.rawValue < b.rawValue }

            /// Under 30 days, under 180, under 365, a year or more.
            static let bounds: [TimeInterval] = [30 * 86_400, 180 * 86_400, 365 * 86_400]

            var pair: PersonaPalette.Pair {
                switch self {
                case .recent: PersonaPalette.Pair(light: 0xE6EEF7, dark: 0x262B32)
                case .months: PersonaPalette.Pair(light: 0xBFD5EE, dark: 0x22405F)
                case .halfYear: PersonaPalette.Pair(light: 0x8DB5E0, dark: 0x1F5187)
                case .year: PersonaPalette.Pair(light: 0x5D93D0, dark: 0x1D62B0)
                }
            }

            var color: Color { pair.color }
        }

        /// A box whose age is not known in the Last Used tint.
        static let ageUnknown = other

        // MARK: Ink and marks

        /// Names and sizes on every fill.
        static let ink = PersonaPalette.Pair(light: 0x1C1C1E, dark: 0xF2F2F7)
        /// The line between neighbours: the page shows through.
        static let separator = Color(light: 0xFFFFFF, dark: 0x0B0B0C)
        /// The cloud side's ground, so an empty or short cloud side still reads as a place.
        static let cloudGround = Color(light: 0xF2F6FB, dark: 0x161B22)
        /// How strongly a selected box's hatch covers it (its "what if" ghost).
        static let ghostHatch = 0.45
        /// A dimmed box while another is hovered or dragged.
        static let dimmed = 0.55
        /// The ribbon across the channel.
        static let ribbonFill = 0.22

        // MARK: Layout (points)

        /// The title strip of a box that draws its children inside.
        static let titleStrip: CGFloat = 22
        /// The frame around a box's children.
        static let inset: CGFloat = 2
        /// Smaller than this is not drawn.
        static let smallest: CGFloat = 4
        /// Room a box needs to draw its children: wide and tall enough at the first level, more deeper down.
        static let nestWidth: CGFloat = 72
        static let nestWidthDeep: CGFloat = 96
        static let nestHeight: CGFloat = 28
        static let nestHeightDeep: CGFloat = 50
        /// A leaf this big writes its name larger, with the size under it.
        static let bigLeafWidth: CGFloat = 160
        static let bigLeafHeight: CGFloat = 72
        /// A box at least this big on both sides gets its own hit target (tap, menu, drag, VoiceOver); the 200 largest.
        static let hitTarget: CGFloat = 24
        static let hitTargetCap = 200
        /// A free strip narrower than this is laid out as an ordinary box.
        static let freeStripLeast: CGFloat = 28
        /// Between the local and the cloud side, where the ribbons run.
        static let channel: CGFloat = 28
        /// The cloud side is never narrower (wide) or shorter (stacked) than this; it shortens instead.
        static let cloudLeast: CGFloat = 120
        /// Beyond this share of the width the cloud side is compressed and shows its "1 : N" chip.
        static let cloudMostShare: CGFloat = 0.45
        /// A box over a year old this big on both sides carries the cold glyph.
        static let glyphLeast: CGFloat = 44
        /// Label padding in a leaf, a big leaf, and between a name and its size.
        static let labelInset: CGFloat = 6
        static let labelInsetBig: CGFloat = 10
        static let labelGap: CGFloat = 4
        /// Under this width the map stacks (cloud below) and nests one level less.
        static let compactWidth: CGFloat = 560
        /// The map's height: a share of its width between a floor and a ceiling.
        static let heightRatio: CGFloat = 9 / 16
        static let heightLeast: CGFloat = 260
        static let heightMost: CGFloat = 520
        static let compactHeightRatio: CGFloat = 1.05
        static let compactHeightLeast: CGFloat = 300
        static let compactHeightMost: CGFloat = 440
        /// Levels drawn below the focused box: three, two on a narrow map.
        static let depth = 3
        static let compactDepth = 2
        /// The incoming twins' and a free strip's dashes.
        static let dash: [CGFloat] = [4, 3]
        static let hatchStep: CGFloat = 6
        /// The selection, focus and suggestion rings.
        static let ring: CGFloat = 2
        /// The cloud badge on a local box holding offloaded items.
        static let badge: CGFloat = 16
        static let badgeLeast: CGFloat = 40
        /// The goal slider's steps.
        static let goalStep: Double = 1_073_741_824

        // MARK: Motion

        /// A zoom: the new level fades and grows out of the box it was (160 ms); Reduce Motion cross-fades.
        static let zoom = Animation.easeOut(duration: 0.16)
        /// How often a scan in progress or an offload job is asked again while on screen.
        static let poll: Duration = .seconds(1)
    }
}
