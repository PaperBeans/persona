import CoreGraphics

/// How ↓ (Jump to Latest) takes the chat to its newest end (#3027): fast from anywhere, as in Telegram and iMessage. A
/// tap first stops the list where it is, glide included (`ChatView`); then, by how far the newest end is:
/// - there already: nothing moves;
/// - within `leapBeyond` screens: one short animated scroll the whole way;
/// - further: the list is put `leapTo` screens short of the newest at once, and that last stretch animates. A scroll
///   through the whole history took seconds and laid out every row on the way; this one draws one screen of them.
/// The flipped list keeps its newest end at offset 0, so the place it is put is exact, however long the chat.
public nonisolated enum ChatJump: Equatable, Sendable {
    /// At the newest end already.
    case none
    /// An animated scroll the whole way, lasting `seconds`.
    case glide(seconds: Double)
    /// Put the list `gap` pt short of the newest end at once, then an animated scroll lasting `seconds`.
    case leap(gap: CGFloat, seconds: Double)

    /// Closer than this (pt) is there: the list's own rounding.
    public static let there: CGFloat = 0.5
    /// Up to this many screens away, the whole way animates.
    public static let leapBeyond: CGFloat = 1.5
    /// From further away, the list is put this many screens short of the newest end, and that much animates.
    public static let leapTo: CGFloat = 1
    /// The animated scroll's length: a short hop is quick, a screen or more takes a little longer so the eye follows it.
    public static let shortest = 0.22
    public static let longest = 0.36

    /// The plan for a list `gap` pt from its newest end, whose screen is `screen` pt tall (0: not measured yet).
    public static func plan(gap: CGFloat, screen: CGFloat) -> ChatJump {
        guard gap > there else { return .none }
        guard screen > 0 else { return .glide(seconds: longest) }
        if gap <= screen * leapBeyond { return .glide(seconds: seconds(gap, screen: screen)) }
        let near = screen * leapTo
        return .leap(gap: near, seconds: seconds(near, screen: screen))
    }

    /// How far along the animated scroll is (0…1) at `time` (0…1 of its length): it starts at speed and settles
    /// (a cubic ease-out), as a list let go of does; a tap during a glide never sees the list stop and start again.
    public static func progress(_ time: Double) -> Double {
        let t = min(max(time, 0), 1)
        let rest = 1 - t
        return 1 - rest * rest * rest
    }

    /// The same plan between any two offsets of a list, either way (#5076): every animated jump of the chat runs this
    /// way, not only ↓. Where the list is put before its animated part starts (`from` when the whole way animates, a
    /// screen short of `to` when it is far, `to` itself when it is there already) and how long that part lasts.
    public static func path(from: CGFloat, to: CGFloat, screen: CGFloat) -> (start: CGFloat, seconds: Double) {
        let gap = from - to
        switch plan(gap: abs(gap), screen: screen) {
        case .none: return (to, 0)
        case .glide(let s): return (from, s)
        case .leap(let near, let s): return (gap < 0 ? to - near : to + near, s)
        }
    }

    /// `y` on the screen's pixel grid. A list moved to whole pixels draws nothing again: its rows' drawings are
    /// composited where they are. SwiftUI's own animated scroll stops at fractions of a pixel, and every frame it
    /// drew every text on screen again for its new offset (#5076).
    public static func pixel(_ y: CGFloat, scale: CGFloat) -> CGFloat {
        guard scale > 0 else { return y }
        return (y * scale).rounded() / scale
    }

    /// How long an animated scroll over `distance` lasts: from `shortest` for a few lines to `longest` for a screen or more.
    public static func seconds(_ distance: CGFloat, screen: CGFloat) -> Double {
        guard screen > 0 else { return longest }
        let part = Double(min(max(distance / screen, 0), 1))
        return shortest + (longest - shortest) * part
    }
}
