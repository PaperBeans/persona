import SwiftUI

/// A run of tool calls folded into one line of chips — "Read 3 files · Search 2× · Edit Provider.swift +6 −3" —
/// with a chevron (a spinner while a call runs). A chip is one `Text` on a box, wrapped by a flow layout; there is
/// a chip per kind of tool, not per call, so a run of 200 commands is still one chip. Tapping and what opening shows
/// belong to the host.
public struct ToolChipGroup: View {
    public struct Chip: Hashable, Sendable {
        /// An SF Symbol.
        public var symbol: String
        public var label: String
        /// The argument: a file, a pattern, a command, or a count.
        public var detail: String?
        public var monospacedDetail: Bool
        public var additions: Int?
        public var deletions: Int?
        public var isError: Bool
        /// Words give way at their end; a path or a name in its middle, where its start and its end both say which.
        public var truncatesTail: Bool

        public init(
            symbol: String,
            label: String,
            detail: String? = nil,
            monospacedDetail: Bool = false,
            additions: Int? = nil,
            deletions: Int? = nil,
            isError: Bool = false,
            truncatesTail: Bool = false
        ) {
            self.symbol = symbol
            self.label = label
            self.detail = detail
            self.monospacedDetail = monospacedDetail
            self.additions = additions
            self.deletions = deletions
            self.isError = isError
            self.truncatesTail = truncatesTail
        }
    }

    var chips: [Chip]
    var isOpen: Bool
    var isRunning: Bool
    // No `@Environment(\.self)` to resolve colors: it re-runs the body on every environment change, and in a
    // scrolling list that was most frames. The renderer's context resolves them.
    @Environment(\.buiTheme) private var theme
    @ScaledMetric(relativeTo: .footnote) private var baseSize: CGFloat = 12.5

    public init(_ chips: [Chip], isOpen: Bool, isRunning: Bool = false) {
        self.chips = chips
        self.isOpen = isOpen
        self.isRunning = isRunning
    }

    public var body: some View {
        let size = baseSize * theme.typeScale
        let pad = (size * 0.3).rounded()
        HStack(alignment: .top, spacing: 8) {
            ZStack {
                if isRunning {
                    BUISpinner()
                } else {
                    BUIIcon(.chevronDown, size: 12, lineWidth: 2.2)
                        .rotationEffect(.degrees(isOpen ? 0 : -90))
                }
            }
            .frame(width: 16, height: size * 1.2 + 2 * pad)
            .foregroundStyle(theme.colors.ink3)
            BUIFlowLayout(spacing: 6, lineSpacing: 6) {
                ForEach(chips.indices, id: \.self) { index in
                    chip(chips[index], size: size, pad: pad)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(chips.map { [$0.label, $0.detail].compactMap { $0 }.joined(separator: " ") }.joined(separator: "，"))
        .accessibilityValue(theme.strings.expanded(isOpen))
    }

    private func chip(_ chip: Chip, size: CGFloat, pad: CGFloat) -> some View {
        let shape = RoundedRectangle(cornerRadius: theme.radii.chip, style: .continuous)
        return text(chip, size: size)
            .lineLimit(1)
            .truncationMode(chip.truncatesTail ? .tail : .middle)
            .padding(.horizontal, 6)
            .padding(.vertical, pad)
            .background(chip.isError ? theme.colors.redTint : theme.colors.field, in: shape)
            .overlay(shape.strokeBorder(chip.isError ? theme.colors.red.opacity(0.45) : theme.colors.line, lineWidth: 1))
    }

    private func text(_ chip: Chip, size: CGFloat) -> Text {
        let icon = Text(Image(systemName: chip.symbol))
            .font(.system(size: size * 0.82, weight: .semibold))
            .foregroundStyle(chip.isError ? theme.colors.red : theme.colors.ink3)
        var t = Text("\(icon) \(Text(chip.label).font(.system(size: size, weight: .medium)).foregroundStyle(theme.colors.ink))")
        if let detail = chip.detail, !detail.isEmpty {
            // `.monospaced()` on the font, not a design: a host's `.fontDesign` would replace the design.
            let font: Font = chip.monospacedDetail ? .system(size: size * 0.92).monospaced() : .system(size: size)
            t = Text("\(t) \(Text(detail).font(font).foregroundStyle(theme.colors.ink2))")
        }
        if let additions = chip.additions {
            t = Text("\(t) \(Text("+\(additions)").font(.system(size: size * 0.92).monospacedDigit()).foregroundStyle(theme.colors.green))")
        }
        if let deletions = chip.deletions, deletions > 0 {
            t = Text("\(t) \(Text("−\(deletions)").font(.system(size: size * 0.92).monospacedDigit()).foregroundStyle(theme.colors.red))")
        }
        return t
    }
}
