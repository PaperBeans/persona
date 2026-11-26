import SwiftUI

/// A group of buttons side by side in equal widths while every label fits its share whole, and one above the other,
/// the first on top and each across the row, when one doesn't (#6151; `docs/design/DESIGN_SYSTEM.md` 1.2 rule 3 (c)).
/// It goes by the measured widths, not by the text size: a short label stays beside its neighbor at sizes where a
/// longer one, or a longer translation, has to stack. A button tells how narrow it can be with its words whole through
/// its label's `buttonLabelLine()`; one whose label doesn't is taken to fit. A single button takes the row's width.
///
/// `packed` (a To-dos row's answers, #10465: one prominent choice and Options ⌄): each at its own width from the leading
/// edge, `spacing` apart, while all fit whole on one line; else one above the other, the first on top, each at its own
/// width. A label is never cut, so a row's answers stack where a long translation or a large text size needs it.
struct StackingButtonRow: Layout {
    var spacing: CGFloat = Theme.Space.m
    var stackedSpacing: CGFloat = Theme.Space.s
    var packed = false

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        guard !subviews.isEmpty else { return .zero }
        if packed { return packedSize(proposal, subviews) }
        let width = proposal.width.flatMap { $0.isFinite ? $0 : nil } ?? idealWidth(subviews)
        if sideBySide(width, subviews) {
            let offer = ProposedViewSize(width: share(width, subviews.count), height: proposal.height)
            return CGSize(width: width, height: subviews.map { $0.sizeThatFits(offer).height }.max() ?? 0)
        }
        let offer = ProposedViewSize(width: width, height: nil)
        let heights = subviews.map { $0.sizeThatFits(offer).height }
        return CGSize(width: width, height: heights.reduce(0, +) + stackedSpacing * CGFloat(subviews.count - 1))
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        if packed { return placePacked(in: bounds, subviews) }
        if sideBySide(bounds.width, subviews) {
            // As an HStack places them: equal shares, centered on the row's middle.
            let each = share(bounds.width, subviews.count)
            var x = bounds.minX
            for s in subviews {
                s.place(at: CGPoint(x: x, y: bounds.midY), anchor: .leading,
                        proposal: ProposedViewSize(width: each, height: proposal.height))
                x += each + spacing
            }
            return
        }
        var y = bounds.minY
        for s in subviews {
            let height = s.sizeThatFits(ProposedViewSize(width: bounds.width, height: nil)).height
            s.place(at: CGPoint(x: bounds.minX, y: y), proposal: ProposedViewSize(width: bounds.width, height: height))
            y += height + stackedSpacing
        }
    }

    // MARK: Packed

    /// Each at its own width, the widest the row's width: what `packed` lays out.
    private func sizes(_ subviews: Subviews, width: CGFloat) -> [CGSize] {
        subviews.map { s in
            let ideal = s.sizeThatFits(.unspecified)
            return ideal.width <= width ? ideal : s.sizeThatFits(ProposedViewSize(width: width, height: nil))
        }
    }

    private func packedFits(_ sizes: [CGSize], width: CGFloat) -> Bool {
        sizes.reduce(0) { $0 + $1.width } + spacing * CGFloat(sizes.count - 1) <= width
    }

    private func packedSize(_ proposal: ProposedViewSize, _ subviews: Subviews) -> CGSize {
        let width = proposal.width.flatMap { $0.isFinite ? $0 : nil } ?? .greatestFiniteMagnitude
        let sizes = sizes(subviews, width: width)
        if packedFits(sizes, width: width) {
            let used = sizes.reduce(0) { $0 + $1.width } + spacing * CGFloat(sizes.count - 1)
            return CGSize(width: min(used, width), height: sizes.map(\.height).max() ?? 0)
        }
        let height = sizes.reduce(0) { $0 + $1.height } + stackedSpacing * CGFloat(sizes.count - 1)
        return CGSize(width: min(sizes.map(\.width).max() ?? 0, width), height: height)
    }

    private func placePacked(in bounds: CGRect, _ subviews: Subviews) {
        let sizes = sizes(subviews, width: bounds.width)
        if packedFits(sizes, width: bounds.width) {
            var x = bounds.minX
            for (s, size) in zip(subviews, sizes) {
                s.place(at: CGPoint(x: x, y: bounds.midY), anchor: .leading, proposal: ProposedViewSize(size))
                x += size.width + spacing
            }
            return
        }
        var y = bounds.minY
        for (s, size) in zip(subviews, sizes) {
            s.place(at: CGPoint(x: bounds.minX, y: y), proposal: ProposedViewSize(size))
            y += size.height + stackedSpacing
        }
    }

    private func share(_ width: CGFloat, _ count: Int) -> CGFloat {
        (width - spacing * CGFloat(count - 1)) / CGFloat(count)
    }

    /// A button's size for a width of zero is the narrowest it shows its words whole (`buttonLabelLine()`).
    private func sideBySide(_ width: CGFloat, _ subviews: Subviews) -> Bool {
        let each = share(width, subviews.count)
        return subviews.allSatisfy { $0.sizeThatFits(ProposedViewSize(width: 0, height: nil)).width <= each }
    }

    private func idealWidth(_ subviews: Subviews) -> CGFloat {
        let widest = subviews.map { $0.sizeThatFits(.unspecified).width }.max() ?? 0
        return widest * CGFloat(subviews.count) + spacing * CGFloat(subviews.count - 1)
    }
}

extension Text {
    /// A button's words on one line (`docs/design/DESIGN_SYSTEM.md` 1.2 rule 3): they may shrink to `minimumScale` for
    /// a small overflow and are never cut. Asked for its narrowest (a width of zero), the line answers with its whole
    /// width at that scale, so a `StackingButtonRow` stacks its buttons before a label would be cut. 0.8 is the
    /// shrink the app's buttons have always allowed.
    func buttonLabelLine(minimumScale: CGFloat = 0.8) -> some View {
        WholeLine(minimumScale: minimumScale) {
            lineLimit(1).minimumScaleFactor(minimumScale)
        }
    }
}

private struct WholeLine: Layout {
    let minimumScale: CGFloat

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        guard let line = subviews.first else { return .zero }
        guard proposal.width == 0 else { return line.sizeThatFits(proposal) }
        let whole = line.sizeThatFits(.unspecified)
        return CGSize(width: (whole.width * minimumScale).rounded(.up), height: whole.height)
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        subviews.first?.place(at: bounds.origin, proposal: ProposedViewSize(bounds.size))
    }
}
