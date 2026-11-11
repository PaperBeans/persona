import SwiftUI

/// One tool call in an agent run.
public struct ToolCall: Identifiable, Sendable {
    public enum Kind: Sendable {
        case think, write, run, read
        case custom(BUIGlyph)
        /// An SF Symbol, for hosts that already have an icon per tool.
        case symbol(String)

        var glyph: BUIGlyph {
            switch self {
            case .think: .sparkle
            case .write: BUIGlyph("M17 3a2.8 2.8 0 1 1 4 4L7.5 20.5 2 22l1.5-5.5z")
            case .run: BUIGlyph("M4 17l6-5-6-5M12 19h8")
            case .read: BUIGlyph("M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8zM14 2v6h6")
            case .custom(let glyph): glyph
            case .symbol: .sparkle
            }
        }
    }

    /// A line of the expanded detail; additions draw green.
    public struct Line: Hashable, Sendable {
        public var text: String
        public var isAddition: Bool

        public init(_ text: String, isAddition: Bool = false) {
            self.text = text
            self.isAddition = isAddition
        }
    }

    public var id: String
    public var kind: Kind
    public var label: String
    /// The argument shown in the chip: a file, a command, a summary.
    public var chip: String
    public var monospacedChip: Bool
    public var detail: [Line]
    public var monospacedDetail: Bool

    public init(
        id: String? = nil,
        _ kind: Kind,
        label: String,
        chip: String,
        monospacedChip: Bool = true,
        detail: [Line] = [],
        monospacedDetail: Bool = false
    ) {
        self.id = id ?? label
        self.kind = kind
        self.label = label
        self.chip = chip
        self.monospacedChip = monospacedChip
        self.detail = detail
        self.monospacedDetail = monospacedDetail
    }
}

/// A changed file with its line counts and an excerpt for the preview.
public struct FileDiff: Identifiable, Sendable {
    public enum Tone: Sendable { case added, removed, context }

    public struct Line: Hashable, Sendable {
        public var text: String
        public var tone: Tone

        public init(_ text: String, _ tone: Tone) {
            self.text = text
            self.tone = tone
        }
    }

    public var id: String { file }
    public var file: String
    public var additions: Int
    public var deletions: Int
    public var lines: [Line]

    public init(file: String, additions: Int, deletions: Int, lines: [Line] = []) {
        self.file = file
        self.additions = additions
        self.deletions = deletions
        self.lines = lines
    }
}

/// An agent run as compact rows of tool calls with inline chips, then chips for the files it
/// changed. Rows expand to show what the tool did; tapping a file chip previews its diff
/// (the original reveals these on hover).
public struct ToolChips: View {
    var summary: String
    var calls: [ToolCall]
    var diffs: [FileDiff]?
    var moreLabel: String?
    var onMore: (() -> Void)?

    @State private var open = true
    @State private var openRows: Set<String>
    @State private var previewFile: String?
    @Environment(\.buiTheme) private var theme

    /// - Parameters:
    ///   - summary: the collapsible run header, e.g. "4 tool calls, 2 messages".
    ///   - calls: the calls made so far; new ones fade up.
    ///   - diffs: files changed, shown once the run ends; `nil` hides the section.
    ///   - expandedCalls / previewedFile: initial interaction state.
    public init(
        summary: String,
        calls: [ToolCall],
        diffs: [FileDiff]? = nil,
        moreLabel: String? = nil,
        expandedCalls: Set<String> = [],
        previewedFile: String? = nil,
        onMore: (() -> Void)? = nil
    ) {
        self.summary = summary
        self.calls = calls
        self.diffs = diffs
        self.moreLabel = moreLabel
        self.onMore = onMore
        _openRows = State(initialValue: expandedCalls)
        _previewFile = State(initialValue: previewedFile)
    }

    public var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header
            VStack(alignment: .leading, spacing: 0) {
                VStack(alignment: .leading, spacing: 4) {
                    ForEach(calls) { call in
                        row(call)
                            .buiEntrance(.fadeUp, duration: 0.3)
                    }
                }
                .padding(.top, 6)
                if let diffs {
                    diffSection(diffs)
                }
            }
            .padding(.bottom, 4)
            .frame(height: open ? nil : 0, alignment: .top)
            .opacity(open ? 1 : 0)
            .clipped()
        }
        .padding(.bottom, 4)
        .frame(maxWidth: 320, minHeight: 220, alignment: .topLeading)
        .overlayPreferenceValue(DiffChipAnchor.self) { anchors in
            GeometryReader { proxy in
                if let file = previewFile, let anchor = anchors[file], let diff = diffs?.first(where: { $0.file == file }) {
                    let rect = proxy[anchor]
                    // Like the original, keep the 288 pt preview inside the available width.
                    DiffPreview(diff: diff)
                        .offset(x: max(0, min(rect.minX, proxy.size.width - 288)), y: rect.maxY + 6)
                        .transition(.asymmetric(insertion: .scale(scale: 0.95, anchor: .topLeading).combined(with: .opacity), removal: .opacity))
                        .id(file)
                }
            }
        }
    }

    private var header: some View {
        ToolRunHeader(summary, isOpen: open) {
            withAnimation(BUICurve.twEaseInOut.animation(duration: 0.3)) { open.toggle() }
        }
    }

    private func row(_ call: ToolCall) -> some View {
        let rowOpen = openRows.contains(call.id)
        return VStack(alignment: .leading, spacing: 0) {
            Button {
                withAnimation(BUICurve.outStrong.animation(duration: 0.3)) {
                    if rowOpen { openRows.remove(call.id) } else { openRows.insert(call.id) }
                }
            } label: {
                ToolChipLabel(call: call, isOpen: rowOpen)
                    .padding(.horizontal, 3)
                    .frame(height: 28 * theme.typeScale)
                .contentShape(.rect(cornerRadius: theme.radii.control))
            }
            .buttonStyle(PressHighlightStyle(radius: theme.radii.control))
            .padding(.horizontal, -3)
            .accessibilityValue(theme.strings.expanded(rowOpen))

            VStack(alignment: .leading, spacing: 2) {
                ForEach(call.detail, id: \.self) { line in
                    Text(line.text)
                        .buiFont(BUITextStyle(size: 11.5, design: call.monospacedDetail ? .monospaced : .default, lineHeight: 1.6))
                        .foregroundStyle(line.isAddition ? theme.colors.green : theme.colors.ink2)
                        .lineLimit(1)
                }
            }
            .padding(.vertical, 2)
            .padding(.leading, 14)
            .frame(maxWidth: .infinity, alignment: .leading)
            .overlay(alignment: .leading) {
                Rectangle().fill(theme.colors.line).frame(width: 1)
            }
            .padding(.leading, 8)
            .padding(.top, 2)
            .padding(.bottom, 4)
            .frame(height: rowOpen ? nil : 0, alignment: .top)
            .opacity(rowOpen ? 1 : 0)
            .clipped()
        }
    }

    private func diffSection(_ diffs: [FileDiff]) -> some View {
        FileDiffChips(diffs, selected: previewFile, moreLabel: moreLabel, onMore: onMore) { diff in
            withAnimation(BUICurve.outStrong.animation(duration: 0.16)) {
                previewFile = previewFile == diff.file ? nil : diff.file
            }
        }
        .padding(.top, 10)
        .overlay(alignment: .top) {
            Rectangle().fill(theme.colors.line).frame(height: 1)
        }
        .padding(.top, 10)
    }
}

/// The collapsible header of a run of tool calls: a chevron (a spinner while the run is going, the words shimmering)
/// and a summary such as "4 tool calls, 2 messages". `accessory` sits at the trailing end.
public struct ToolRunHeader<Label: View, Accessory: View>: View {
    var isOpen: Bool
    var isRunning: Bool
    var action: () -> Void
    var label: Label
    var accessory: Accessory
    @Environment(\.buiTheme) private var theme

    public init(
        isOpen: Bool,
        isRunning: Bool = false,
        action: @escaping () -> Void,
        @ViewBuilder label: () -> Label,
        @ViewBuilder accessory: () -> Accessory
    ) {
        self.isOpen = isOpen
        self.isRunning = isRunning
        self.action = action
        self.label = label()
        self.accessory = accessory()
    }

    public var body: some View {
        HStack(spacing: 6) {
            Button(action: action) {
                ToolRunTitle(isOpen: isOpen, isRunning: isRunning) { label }
                    .padding(.horizontal, 6)
                    .padding(.vertical, 4)
                    .contentShape(.rect(cornerRadius: theme.radii.control))
            }
            .buttonStyle(PressHighlightStyle(radius: theme.radii.control))
            .padding(.horizontal, -6)
            .accessibilityValue(theme.strings.expanded(isOpen))
            Spacer(minLength: 0)
            accessory
        }
    }
}

/// `ToolRunHeader`'s words without its button, for a host that wraps them in its own: the chevron (a spinner while
/// running) and the summary, shimmering while the run goes on.
public struct ToolRunTitle<Label: View>: View {
    var isOpen: Bool
    var isRunning: Bool
    var label: Label
    @Environment(\.buiTheme) private var theme

    private var styled: some View {
        label
            .buiFont(BUITextStyle(size: 12.5))
            .monospacedDigit()
            .lineLimit(1)
    }

    public init(isOpen: Bool, isRunning: Bool = false, @ViewBuilder label: () -> Label) {
        self.isOpen = isOpen
        self.isRunning = isRunning
        self.label = label()
    }

    public var body: some View {
        HStack(spacing: 6) {
            if isRunning {
                BUISpinner(size: 11 * theme.typeScale)
                    .frame(width: 12 * theme.typeScale)
            } else {
                BUIIcon(.chevronDown, size: 12 * theme.typeScale, lineWidth: 2.2)
                    .rotationEffect(.degrees(isOpen ? 0 : -90))
            }
            // The shimmer draws its own inks; settled, the words keep the header's ink-2.
            if isRunning {
                styled.buiShimmer()
            } else {
                styled
            }
        }
        .foregroundStyle(theme.colors.ink2)
    }
}

public extension ToolRunHeader where Label == Text, Accessory == EmptyView {
    init(_ summary: String, isOpen: Bool, isRunning: Bool = false, action: @escaping () -> Void) {
        self.init(isOpen: isOpen, isRunning: isRunning, action: action) { Text(summary) } accessory: { EmptyView() }
    }
}

/// The files a run changed, as chips that wrap: the name in the code face with its green +N and red −N, then an
/// optional "+2 more". Tapping a chip calls back with its file (`ToolChips` opens a diff preview under it).
public struct FileDiffChips: View {
    var diffs: [FileDiff]
    var selected: String?
    var moreLabel: String?
    var onMore: (() -> Void)?
    var onSelect: ((FileDiff) -> Void)?
    @Environment(\.buiTheme) private var theme

    public init(
        _ diffs: [FileDiff],
        selected: String? = nil,
        moreLabel: String? = nil,
        onMore: (() -> Void)? = nil,
        onSelect: ((FileDiff) -> Void)? = nil
    ) {
        self.diffs = diffs
        self.selected = selected
        self.moreLabel = moreLabel
        self.onMore = onMore
        self.onSelect = onSelect
    }

    public var body: some View {
        BUIFlowLayout(spacing: 6) {
            ForEach(Array(diffs.enumerated()), id: \.element.id) { index, diff in
                Button {
                    onSelect?(diff)
                } label: {
                    HStack(spacing: 8) {
                        Text(diff.file)
                            .foregroundStyle(theme.colors.ink)
                            .lineLimit(1)
                            .truncationMode(.middle)
                        if diff.additions > 0 || diff.deletions == 0 {
                            Text("+\(diff.additions)")
                                .foregroundStyle(theme.colors.green)
                        }
                        if diff.deletions > 0 {
                            Text("\u{2212}\(diff.deletions)")
                                .foregroundStyle(theme.colors.red)
                        }
                    }
                    .buiFont(BUITextStyle(size: 11.5, design: .monospaced))
                    .monospacedDigit()
                    .padding(.horizontal, 8)
                    .frame(height: 28 * max(1, theme.typeScale * 0.9))
                    .buiSurface(radius: theme.radii.chip, fill: selected == diff.file ? theme.colors.hover : nil, elevation: .button)
                    .contentShape(.rect(cornerRadius: theme.radii.chip))
                }
                .buttonStyle(.plain)
                .disabled(onSelect == nil)
                .anchorPreference(key: DiffChipAnchor.self, value: .bounds) { [diff.file: $0] }
                .accessibilityLabel("\(diff.file) +\(diff.additions) \u{2212}\(diff.deletions)")
                .buiEntrance(.popIn, duration: 0.25, delay: Double(index) * 0.08)
            }
            if let moreLabel {
                Button {
                    onMore?()
                } label: {
                    Text(moreLabel)
                        .buiFont(BUITextStyle(size: 11.5, design: .monospaced))
                        .foregroundStyle(theme.colors.ink3)
                        .padding(.horizontal, 6)
                        .frame(height: 28 * max(1, theme.typeScale * 0.9))
                }
                .buttonStyle(.plain)
                .disabled(onMore == nil)
                .buiEntrance(.fadeIn, duration: 0.3, delay: Double(diffs.count) * 0.08, curve: .easeOut)
            }
        }
    }
}

/// One tool call as a row of its own, for hosts that lay out each call as a separate list
/// row (a lazy list) instead of a whole run in one `ToolChips`. `accessory` sits after the chip.
/// How `ToolChipRow` draws the argument: `.boxed` is the original's field-colored chip; `.plain` keeps only its
/// type, for long runs of rows where a box on every line gets busy.
public enum ToolChipStyle: Sendable { case boxed, plain }

public struct ToolChipRow<Accessory: View>: View {
    var call: ToolCall
    var isOpen: Bool
    var isRunning: Bool
    var chipStyle: ToolChipStyle
    var action: () -> Void
    var accessory: Accessory
    @Environment(\.buiTheme) private var theme

    public init(
        _ call: ToolCall,
        isOpen: Bool,
        isRunning: Bool = false,
        chipStyle: ToolChipStyle = .boxed,
        action: @escaping () -> Void,
        @ViewBuilder accessory: () -> Accessory
    ) {
        self.call = call
        self.isOpen = isOpen
        self.isRunning = isRunning
        self.chipStyle = chipStyle
        self.action = action
        self.accessory = accessory()
    }

    public var body: some View {
        Button(action: action) {
            HStack(spacing: 8) {
                ToolChipLabel(call: call, isOpen: isOpen, isRunning: isRunning, boxed: chipStyle == .boxed)
                accessory
            }
            .frame(minHeight: 28 * theme.typeScale)
            .contentShape(.rect(cornerRadius: theme.radii.control))
        }
        .buttonStyle(PressHighlightStyle(radius: theme.radii.control))
        .accessibilityValue(theme.strings.expanded(isOpen))
    }
}

public extension ToolChipRow where Accessory == EmptyView {
    init(_ call: ToolCall, isOpen: Bool, isRunning: Bool = false, chipStyle: ToolChipStyle = .boxed, action: @escaping () -> Void) {
        self.init(call, isOpen: isOpen, isRunning: isRunning, chipStyle: chipStyle, action: action) { EmptyView() }
    }
}

/// Icon (a chevron once open, a spinner while running), label and the argument chip.
struct ToolChipLabel: View {
    var call: ToolCall
    var isOpen: Bool
    var isRunning = false
    var boxed = true
    @Environment(\.buiTheme) private var theme

    var body: some View {
        HStack(spacing: 8) {
            ZStack {
                if isRunning {
                    BUISpinner()
                } else {
                    kindIcon
                        .opacity(isOpen ? 0 : 1)
                    BUIIcon(.chevronDown, size: 12, lineWidth: 2.2)
                        .rotationEffect(.degrees(isOpen ? 0 : -90))
                        .opacity(isOpen ? 1 : 0)
                }
            }
            .frame(width: 16, height: 16)
            .foregroundStyle(theme.colors.ink3)
            // Whole when it fits, before the chip; a label wider than the row (an MCP tool's "Server · Tool") ends in "…"
            // instead of pushing the row off the screen.
            Text(call.label)
                .buiFont(BUITextStyle(size: 12.5, weight: .medium))
                .foregroundStyle(theme.colors.ink)
                .lineLimit(1)
                .layoutPriority(1)
            if boxed {
                chip
                    .padding(.horizontal, 6)
                    .frame(maxWidth: .infinity, minHeight: 22 * theme.typeScale, maxHeight: 22 * theme.typeScale, alignment: .leading)
                    .background(theme.colors.field, in: .rect(cornerRadius: theme.radii.chip))
                    .overlay(RoundedRectangle(cornerRadius: theme.radii.chip).strokeBorder(theme.colors.line, lineWidth: 1))
            } else {
                chip
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
        }
    }

    private var chip: some View {
        Text(call.chip)
            .buiFont(BUITextStyle(size: 11.5, design: call.monospacedChip ? .monospaced : .default))
            .foregroundStyle(theme.colors.ink2)
            .lineLimit(1)
            .truncationMode(.tail)
    }

    @ViewBuilder private var kindIcon: some View {
        if case .symbol(let name) = call.kind {
            Image(systemName: name).font(.system(size: 11, weight: .semibold))
        } else {
            BUIIcon(call.kind.glyph, size: 13)
        }
    }
}

struct DiffChipAnchor: PreferenceKey {
    static let defaultValue: [String: Anchor<CGRect>] = [:]
    static func reduce(value: inout [String: Anchor<CGRect>], nextValue: () -> [String: Anchor<CGRect>]) {
        value.merge(nextValue()) { $1 }
    }
}

/// The floating diff excerpt for one file (`w-72 rounded-[10px] bg-surface shadow-overlay`).
struct DiffPreview: View {
    let diff: FileDiff
    @Environment(\.buiTheme) private var theme

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                Text(diff.file)
                    .foregroundStyle(theme.colors.ink2)
                    .lineLimit(1)
                Spacer(minLength: 8)
                HStack(spacing: 4) {
                    Text("+\(diff.additions)").foregroundStyle(theme.colors.green)
                    if diff.deletions > 0 {
                        Text("−\(diff.deletions)").foregroundStyle(theme.colors.red)
                    }
                }
                .monospacedDigit()
            }
            .buiFont(BUITextStyle(size: 11, design: .monospaced))
            .padding(.horizontal, 10)
            .padding(.vertical, 6)
            .overlay(alignment: .bottom) {
                Rectangle().fill(theme.colors.line).frame(height: 1)
            }
            VStack(alignment: .leading, spacing: 0) {
                ForEach(Array(diff.lines.enumerated()), id: \.offset) { _, line in
                    HStack(spacing: 8) {
                        Text(line.tone == .added ? "+" : line.tone == .removed ? "−" : " ")
                            .frame(width: 12, alignment: .leading)
                        Text(line.text)
                            .lineLimit(1)
                        Spacer(minLength: 0)
                    }
                    .buiFont(BUITextStyle(size: 11, design: .monospaced, lineHeight: 1.8))
                    .foregroundStyle(color(line.tone))
                    .padding(.horizontal, 10)
                    .background(tint(line.tone))
                }
            }
            .padding(.vertical, 4)
        }
        .frame(width: 288)
        .buiSurface(radius: theme.radii.card, elevation: .overlay)
    }

    private func color(_ tone: FileDiff.Tone) -> Color {
        switch tone {
        case .added: theme.colors.green
        case .removed: theme.colors.red
        case .context: theme.colors.ink2
        }
    }

    private func tint(_ tone: FileDiff.Tone) -> Color {
        switch tone {
        case .added: theme.colors.greenTint
        case .removed: theme.colors.redTint
        case .context: .clear
        }
    }
}
