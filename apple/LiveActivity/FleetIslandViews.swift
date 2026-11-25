import SwiftUI

/// Persona' agent colors: the dots of StatusPalette (Shared/StatusPalette.swift, which the extension builds too; it does
/// not build the app's Theme). Shared by the widget and the island preview in Settings › Notifications › Dynamic Island.
enum FleetPalette {
    static var needsYou: Color { StatusPalette.needsYou.dot }
    static var working: Color { StatusPalette.working.dot }
    static var unread: Color { StatusPalette.unread.dot }
    /// Small working text (the timer) on the lock screen, which follows the system appearance.
    static var workingText: Color { StatusPalette.working.text }
    /// The empty part of a background workflow's bar (#3437): white on the island's black, the text's color on the lock
    /// screen (black there too in both appearances). At 0.4 it is 3.7:1 on black and the working fill 3.2:1 on it; the
    /// system's linear track was 1.2:1 there, and 3/8 read as a short orange line rather than three eighths.
    static func track(onDark: Bool) -> Color { (onDark ? Color.white : Color.primary).opacity(0.4) }
    /// The app icon's coral (the app's `Theme.brand`): the island's number symbols, nothing to tap.
    static let coral = Palette.coral
    /// System blue, the app's accent: the keyline and the system's action buttons.
    static let accent = Palette.systemBlue
    /// A system blue dark enough for white button labels (4.7:1).
    static let button = Palette.buttonBlue
    /// The pictures of the island in Settings: where the camera sits, and another app's activity (a stand-in).
    static let camera = Color(white: 0.12)
    static let otherApp = Color(white: 0.35)

    static func color(_ phase: FleetState.Phase) -> Color {
        switch phase {
        case .needsYou: needsYou
        case .working: working
        case .done: unread
        case .none: .secondary
        }
    }

    static func color(_ kind: FleetState.Item.Kind) -> Color {
        switch kind {
        case .working: working
        case .unread: unread
        default: needsYou
        }
    }
}

/// The island's symbols, one per meaning (the extension does not build `Theme.Icon`): the counts' symbols are the
/// lock screen's too.
enum FleetSymbol {
    static let needsYou = "exclamationmark.bubble.fill"
    static let working = "gearshape.fill"
    static let unread = "circle.fill"
    static let tpm = "bolt.fill"
    static let tokens = "chart.bar.fill"
    static let requests = "arrow.up.arrow.down"
    static let load = "cpu"
    static let assistant = "sparkle"
    static let timer = "timer"
}

/// A cell that is a symbol and a number: what it shows and in which colors. Counts take their state's color; the
/// numbers (TPM, tokens…) stay white beside a coral symbol.
struct IslandPair {
    var symbol: String
    var text: String
    var tint: Color
    var number: Color = .white
    /// Unread's dot: a smaller symbol than the others.
    var dot = false
    /// What VoiceOver says for the cell when the symbol and the number alone would read wrong: load's "1.1" beside the
    /// CPU symbol is "Load 1.1 per core", not "CPU, 1.1" (#12519). Nil: the symbol and the number, as drawn.
    var spoken: String?

    init?(_ cell: IslandCell) {
        switch cell {
        case let .needsYou(n): self.init(symbol: FleetSymbol.needsYou, text: "\(n)", tint: FleetPalette.needsYou, number: FleetPalette.needsYou)
        case let .working(n): self.init(symbol: FleetSymbol.working, text: "\(n)", tint: FleetPalette.working, number: FleetPalette.working)
        case let .unread(n): self.init(symbol: FleetSymbol.unread, text: "\(n)", tint: FleetPalette.unread, number: FleetPalette.unread, dot: true)
        case let .tpm(n): self.init(symbol: FleetSymbol.tpm, text: FleetText.short(n), tint: FleetPalette.coral)
        case let .tokens(n): self.init(symbol: FleetSymbol.tokens, text: FleetText.short(n), tint: FleetPalette.coral)
        case let .requests(n): self.init(symbol: FleetSymbol.requests, text: FleetText.short(n), tint: FleetPalette.coral)
        // Load per core beside the CPU symbol ("1.1"), as Machines writes it: "110%" read as CPU use (#12519).
        case let .load(_, p):
            self.init(symbol: FleetSymbol.load, text: FleetText.perCore(percent: p), tint: FleetPalette.coral, spoken: FleetText.stat(cell))
        case .assistant, .timer, .empty: return nil
        }
    }

    private init(symbol: String, text: String, tint: Color, number: Color = .white, dot: Bool = false, spoken: String? = nil) {
        self.symbol = symbol
        self.text = text
        self.tint = tint
        self.number = number
        self.dot = dot
        self.spoken = spoken
    }
}

/// The compact Dynamic Island's left side: the persona in the fleet's state, then the left cell. Beside the face a
/// Needs You / Working count is just its number in its color (the face already says which: red ring, working eyes).
struct IslandLeading: View {
    var state: FleetState
    var stale: Bool
    var look: FleetLook = .saved

    var body: some View {
        HStack(spacing: IslandMetrics.faceGap) {
            FleetFace(size: IslandMetrics.face, state: FleetPersona.state(state, stale: stale), look: look)
            IslandCellView(cell: IslandCell.sides(state).left, bare: true)
        }
        .fixedSize()
    }
}

/// One side of the compact Dynamic Island: a symbol and a short number. `small` is the minimal island's size; `bare`
/// drops the Needs You / Working symbol (next to the persona, which shows it).
struct IslandCellView: View {
    var cell: IslandCell
    var small = false
    var bare = false

    var body: some View {
        if bare, let pair = IslandPair(cell), Self.saysItsState(cell) {
            IslandNumber(pair: pair, small: small)
        } else if let pair = IslandPair(cell) {
            IslandPairView(pair: pair, small: small)
        } else {
            switch cell {
            case let .assistant(on):
                Image(systemName: FleetSymbol.assistant)
                    .font(.system(size: small ? IslandMetrics.smallStar : IslandMetrics.star, weight: .bold))
                    .foregroundStyle(on ? FleetPalette.coral : Color.white.opacity(0.4))
            case let .timer(since):
                HStack(spacing: IslandMetrics.gap) {
                    Image(systemName: FleetSymbol.timer).font(.system(size: IslandMetrics.timerSymbol, weight: .bold))
                        .foregroundStyle(FleetPalette.working)
                    Text(timerInterval: since...Date.distantFuture, countsDown: false, showsHours: false)
                        .font(.system(size: IslandMetrics.timerNumber, weight: .semibold)).monospacedDigit().lineLimit(1)
                        .frame(maxWidth: IslandMetrics.timerWidth, alignment: .leading)
                }
            default:
                EmptyView()
            }
        }
    }

    /// Needs You / Working: the counts the persona's face shows too (red ring, working eyes).
    static func saysItsState(_ cell: IslandCell) -> Bool {
        switch cell {
        case .needsYou, .working: true
        default: false
        }
    }
}

/// The symbol and the number side by side, at their full size.
struct IslandPairView: View {
    var pair: IslandPair
    var small: Bool

    var body: some View {
        HStack(spacing: small ? IslandMetrics.smallGap : IslandMetrics.gap) {
            Image(systemName: pair.symbol)
                .font(pair.dot ? .system(size: small ? IslandMetrics.smallDot : IslandMetrics.dot) : IslandMetrics.symbolFont(small: small))
                .foregroundStyle(pair.tint)
                .accessibilityHidden(pair.spoken != nil) // the spoken words say what the symbol shows
            IslandNumber(pair: pair, small: small)
        }
    }
}

/// The number, never shortened or shrunk. VoiceOver reads the pair's words when it has them.
struct IslandNumber: View {
    var pair: IslandPair
    var small: Bool
    var text: String?

    var body: some View {
        Text(text ?? pair.text).font(IslandMetrics.numberFont(small: small)).monospacedDigit()
            .foregroundStyle(pair.number).lineLimit(1).fixedSize()
            .accessibilityLabel(Text(verbatim: text ?? pair.spoken ?? pair.text))
    }
}

/// The Fleet in one glyph: the minimal island (another app's activity beside ours, StandBy) and a compact side the
/// system limits in width (iPhone 18 Pro beside another app's activity, landscape, iPhone Duo), where the compact
/// island's other side is left empty and the system hides it. The most urgent count (Needs You > Working > Unread), whole
/// (IslandFitTests): its symbol and number while they fit, in a limited side's 28 pt circle the number in a ring of its
/// state's color from 10 on, and 99+ in its color past 99. Chosen by the count: outside limited mode the island offers
/// no width, it grows with what it shows (up to 35 pt for the minimal island), so `ViewThatFits` would always pick
/// the first.
struct IslandGlyph: View {
    var cell: IslandCell
    var limited: Bool

    enum Look: Equatable { case pair, ring, many }

    static func look(_ count: Int, limited: Bool) -> Look {
        if count > 99 { return .many }
        return limited && count > 9 ? .ring : .pair
    }

    var body: some View {
        if let count = cell.count, let pair = IslandPair(cell) {
            switch Self.look(count, limited: limited) {
            case .pair: IslandPairView(pair: pair, small: true)
            case .ring: IslandRing(pair: pair)
            case .many: IslandNumber(pair: pair, small: true, text: FleetText.many)
            }
        } else {
            IslandCellView(cell: cell, small: true)
        }
    }
}

/// The minimal island and a compact side limited in width: the most urgent count (`IslandGlyph`), or, with nothing
/// there to count or show (only the assistant answering, say), the persona in the fleet's state rather than an empty
/// island.
struct IslandMinimal: View {
    var state: FleetState
    var stale: Bool
    var limited: Bool
    var look: FleetLook = .saved

    var body: some View {
        let cell = IslandCell.minimal(state)
        if cell == .empty {
            FleetFace(size: IslandMetrics.limitedContent, state: FleetPersona.state(state, stale: stale), look: look)
        } else {
            IslandGlyph(cell: cell, limited: limited)
        }
    }
}

/// A count in a ring of its state's color, for a limited side where its symbol does not fit: the ring says it is a
/// count, its color which one.
struct IslandRing: View {
    var pair: IslandPair

    var body: some View {
        IslandNumber(pair: pair, small: true)
            .frame(width: IslandMetrics.limitedContent, height: IslandMetrics.limitedContent)
            .overlay(Circle().strokeBorder(pair.tint, lineWidth: IslandMetrics.ring))
    }
}

/// The compact island as a picture, for Settings › Notifications › Dynamic Island: the two sides around a camera on a black pill.
struct IslandPreview: View {
    var state: FleetState
    var look: FleetLook = .saved

    var body: some View {
        let sides = IslandCell.sides(state)
        HStack(spacing: 0) {
            IslandLeading(state: state, stale: false, look: look).frame(maxWidth: .infinity, alignment: .leading)
            IslandCamera()
            IslandCellView(cell: sides.right).frame(maxWidth: .infinity, alignment: .trailing)
        }
        .padding(.horizontal, IslandMetrics.previewInset)
        .frame(width: IslandMetrics.previewWidth, height: IslandMetrics.previewHeight)
        .background(Capsule().fill(.black))
        .environment(\.colorScheme, .dark)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(interfaceText("\(IslandPreview.describe(sides.left)), \(IslandPreview.describe(sides.right))"))
    }

    static func describe(_ cell: IslandCell) -> String {
        switch cell {
        case let .needsYou(n): StateName.count(.needsYou, n)
        case let .working(n): StateName.count(.working, n)
        case let .unread(n): StateName.count(.unread, n)
        case .timer: interfaceText(LocalizedStringResource("Timer", comment: "VoiceOver: the island side shows a timer"))
        case .empty: interfaceText(LocalizedStringResource("Empty", comment: "VoiceOver: the island side is empty"))
        default: FleetText.stat(cell) ?? ""
        }
    }
}

/// The island beside another app's activity, for Settings › Notifications › Dynamic Island: our glyph by the camera, the other app's activity
/// (a gray stand-in) in a circle of its own. Which of the two sits by the camera is the system's choice; ours reads
/// the same in either place.
struct IslandSharedPreview: View {
    var state: FleetState

    var body: some View {
        let cell = IslandCell.minimal(state)
        HStack(spacing: IslandMetrics.previewGap) {
            HStack(spacing: 0) {
                IslandGlyph(cell: cell, limited: true)
                    .frame(width: IslandMetrics.limited)
                Spacer(minLength: 0)
                IslandCamera()
            }
            .padding(.horizontal, IslandMetrics.previewInset / 2)
            .frame(width: IslandMetrics.previewSharedWidth, height: IslandMetrics.previewHeight)
            .background(Capsule().fill(.black))
            Circle().fill(FleetPalette.otherApp)
                .frame(width: IslandMetrics.previewOther, height: IslandMetrics.previewOther)
                .frame(width: IslandMetrics.previewHeight, height: IslandMetrics.previewHeight)
                .background(Circle().fill(.black))
        }
        .environment(\.colorScheme, .dark)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(interfaceText("Shared with another app: \(IslandPreview.describe(cell))"))
    }
}

/// Where the camera sits, in the pictures of the island.
private struct IslandCamera: View {
    var body: some View {
        Capsule().fill(FleetPalette.camera)
            .frame(width: IslandMetrics.previewCamera.width, height: IslandMetrics.previewCamera.height)
    }
}

/// The numbers the device chose for its island (TPM, today's tokens, load, the assistant), right of the counts on the
/// expanded island and the Lock Screen. Load in words ("Load 1.4 per core", a chosen machine's name for "Load") is about
/// twice as wide as the percent it replaced (#12519) and, beside a second number, wider than the expanded island's
/// corner: where the words don't fit whole, load is the compact island's CPU symbol and figure, its words still spoken
/// (IslandFitTests.statsLineFitsTheExpandedCorner).
struct FleetStatsLine: View {
    var state: FleetState
    var onDark = false

    /// Between two numbers.
    static let spacing: CGFloat = 8

    static func count(_ s: FleetState) -> Int { [s.left, s.right].filter { $0 != .status && $0 != .none }.count }

    var body: some View {
        let cells = [state.left, state.right].filter { $0 != .status && $0 != .none }.map { IslandCell.cell($0, state, status: nil) }
        ViewThatFits(in: .horizontal) {
            row(cells, loadFigure: false)
            row(cells, loadFigure: true)
        }
    }

    private func row(_ cells: [IslandCell], loadFigure: Bool) -> some View {
        HStack(spacing: Self.spacing) {
            ForEach(Array(cells.enumerated()), id: \.offset) { _, cell in
                if case let .timer(since) = cell {
                    FleetTimer(since: since).font(.caption.weight(.semibold)).foregroundStyle(onDark ? FleetPalette.working : FleetPalette.workingText)
                        .frame(maxWidth: 52, alignment: .trailing)
                } else if loadFigure, case let .load(_, percent) = cell {
                    FleetLoadFigure(percent: percent, spoken: FleetText.stat(cell), onDark: onDark)
                } else if let text = FleetText.stat(cell) {
                    FleetStatText(text: text, onDark: onDark)
                }
            }
        }
    }
}

/// One number of the stats line in words ("42M today").
struct FleetStatText: View {
    var text: String
    var onDark = false

    var body: some View {
        Text(text).font(.caption.weight(.semibold)).monospacedDigit().lineLimit(1).minimumScaleFactor(0.8)
            .foregroundStyle(Self.ink(onDark))
    }

    static func ink(_ onDark: Bool) -> Color { onDark ? Color.white.opacity(0.75) : Color.secondary }
}

/// Load where its words don't fit the stats line: the compact island's CPU symbol and the figure per core ("1.4"),
/// read as the words ("Load 1.4 per core").
struct FleetLoadFigure: View {
    var percent: Int
    var spoken: String?
    var onDark = false

    var body: some View {
        HStack(spacing: 2) {
            Image(systemName: FleetSymbol.load)
            Text(FleetText.perCore(percent: percent)).monospacedDigit()
        }
        .font(.caption.weight(.semibold)).lineLimit(1)
        .foregroundStyle(FleetStatText.ink(onDark))
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(Text(verbatim: spoken ?? FleetText.perCore(percent: percent)))
    }
}

/// How long the longest turn has run, on the stats line and the rows.
struct FleetTimer: View {
    var since: Date
    var body: some View {
        Text(timerInterval: since...Date.distantFuture, countsDown: false, showsHours: true)
            .monospacedDigit()
            .multilineTextAlignment(.trailing)
            .lineLimit(1)
    }
}
