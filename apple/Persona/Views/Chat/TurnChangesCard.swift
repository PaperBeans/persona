import SwiftUI
#if os(iOS)
import UIKit
#endif

/// Opens branch review (optionally focused on one file, with fallback to `FileViewer`) from `TurnChangesCard`
/// without passing changing closures through SwiftUI's environment (P5, matching `FileOpener`).
@MainActor final class TurnReviewOpener {
    let openBranchReview: () -> Void
    let openChangedFile: (TurnChangedFile) -> Void

    init(openBranchReview: @escaping () -> Void, openChangedFile: @escaping (TurnChangedFile) -> Void) {
        self.openBranchReview = openBranchReview
        self.openChangedFile = openChangedFile
    }
}

extension EnvironmentValues {
    /// Opens branch review or a changed file from a completed Agent turn's Changed Files card (set by `ChatView`).
    @Entry var openTurnReview: TurnReviewOpener? = nil
}

/// Compact Changed Files card shown after the final answer of a completed Agent exchange (#12016).
/// Reuses existing design-system tokens, `CodeChangeCounts`, `FoldToggle`, and `FileViewer.symbol(for:)`.
struct TurnChangesCard: View, Equatable {
    nonisolated let pinKey: String
    nonisolated let changes: TurnChangeSet
    nonisolated let canReview: Bool

    @Environment(\.openTurnReview) private var openTurnReview
    @Environment(\.chatPin) private var pin
    @Environment(\.dynamicTypeSize) private var typeSize
    @State private var expanded = false

    nonisolated static func == (lhs: Self, rhs: Self) -> Bool {
        lhs.pinKey == rhs.pinKey && lhs.changes == rhs.changes && lhs.canReview == rhs.canReview
    }

    var body: some View {
        let visible = TurnChanges.visibleFiles(in: changes, expanded: expanded)
        let hidden = changes.files.count - visible.count
        VStack(alignment: .leading, spacing: Theme.Space.flush) {
            header
                .padding(.horizontal, Theme.Space.l)
                .padding(.vertical, Theme.Space.m)
            Divider()
            VStack(alignment: .leading, spacing: Theme.Space.flush) {
                ForEach(visible) { file in
                    fileRow(file)
                }
                if changes.files.count > TurnChanges.initialVisibleCount {
                    FoldToggle(
                        open: expanded,
                        prefix: "turn-changes",
                        tint: .secondary,
                        fullRow: true,
                        title: expanded ? String(localized: "Show Less [Bubble Fold]") : showMoreTitle(hidden)
                    ) {
                        Haptics.tap()
                        toggleInPlace(pin, pinKey, animation: Theme.Motion.state, closing: expanded) {
                            expanded.toggle()
                        }
                    }
                }
            }
            .padding(.horizontal, Theme.Space.m)
            .padding(.vertical, Theme.Space.s)
        }
        .background(Theme.card, in: .rect(cornerRadius: Theme.Radius.card, style: .continuous))
        .overlay {
            RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous)
                .strokeBorder(Theme.cardStroke, lineWidth: Theme.Size.hairline)
        }
        .chatPinned(pinKey)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("turn-changes.card")
    }

    @ViewBuilder private var header: some View {
        if typeSize.isAccessibilitySize {
            VStack(alignment: .leading, spacing: Theme.Space.s) {
                headerSummary
                if canReview { reviewButton }
            }
        } else {
            HStack(spacing: Theme.Space.m) {
                headerSummary
                Spacer(minLength: Theme.Space.s)
                if canReview { reviewButton }
            }
        }
    }

    private var headerSummary: some View {
        HStack(spacing: Theme.Space.m) {
            ThemedIcon(Theme.Icon.reviewChanges)
                .font(Theme.Font.secondary)
                .foregroundStyle(.secondary)
                .scaledFrame(width: Theme.Size.wellL, height: Theme.Size.wellL)
                .background(Theme.Fill.well, in: .rect(cornerRadius: Theme.Radius.control, style: .continuous))
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                Text(headerTitle)
                    .font(Theme.Font.secondaryMedium)
                    .foregroundStyle(.primary)
                    .lineLimit(2)
                if changes.exact {
                    CodeChangeCounts(additions: changes.add, deletions: changes.del)
                }
            }
            .frame(minWidth: Theme.Space.flush, alignment: .leading)
        }
    }

    private var reviewButton: some View {
        Button {
            Haptics.tap()
            openTurnReview?.openBranchReview()
        } label: {
            Text("Review [Turn Changes]").lineLimit(1)
        }
        .themedButton()
        .accessibilityIdentifier("turn-changes.review")
    }

    private func fileRow(_ file: TurnChangedFile) -> some View {
        let showsCounts = file.exact && (file.add > 0 || file.del > 0)
        return Button {
            Haptics.tap()
            openTurnReview?.openChangedFile(file)
        } label: {
            Group {
                if typeSize.isAccessibilitySize {
                    VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                        HStack(alignment: .firstTextBaseline, spacing: Theme.Space.s) {
                            ThemedIcon(FileViewer.symbol(for: file.path))
                                .font(Theme.Font.caption)
                                .foregroundStyle(.secondary)
                                .scaledFrame(width: Theme.Size.markM, height: Theme.Size.markM)
                                .accessibilityHidden(true)
                            Text(verbatim: file.shown)
                                .font(Theme.Font.caption)
                                .foregroundStyle(.primary)
                                .lineLimit(2)
                                .truncationMode(.middle)
                                .frame(maxWidth: .infinity, alignment: .leading)
                        }
                        if showsCounts {
                            CodeChangeCounts(
                                additions: file.add > 0 ? file.add : nil,
                                deletions: file.del > 0 ? file.del : nil
                            )
                        }
                    }
                    .padding(.vertical, Theme.Space.xs)
                } else {
                    HStack(spacing: Theme.Space.s) {
                        ThemedIcon(FileViewer.symbol(for: file.path))
                            .font(Theme.Font.caption)
                            .foregroundStyle(.secondary)
                            .scaledFrame(width: Theme.Size.markM, height: Theme.Size.markM)
                            .accessibilityHidden(true)
                        Text(verbatim: file.shown)
                            .font(Theme.Font.caption)
                            .foregroundStyle(.primary)
                            .lineLimit(1)
                            .truncationMode(.middle)
                            .frame(maxWidth: .infinity, alignment: .leading)
                        if showsCounts {
                            CodeChangeCounts(
                                additions: file.add > 0 ? file.add : nil,
                                deletions: file.del > 0 ? file.del : nil
                            )
                        }
                    }
                }
            }
            .padding(.horizontal, Theme.Space.s)
            .scaledFrame(minHeight: Theme.Size.tapTarget)
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .accessibilityIdentifier("turn-changes.file")
        #if os(iOS)
        .contextMenu {
            Button {
                UIPasteboard.general.string = file.path
            } label: {
                Label("Copy Path", systemImage: Theme.Icon.copy)
            }
        }
        #endif
    }

    private var headerTitle: String {
        let count = changes.files.count
        return count == 1 ? String(localized: "Changed 1 file") : String(localized: "Changed \(count) files")
    }

    private func showMoreTitle(_ count: Int) -> String {
        count == 1 ? String(localized: "Show 1 more file") : String(localized: "Show \(count) more files")
    }
}
