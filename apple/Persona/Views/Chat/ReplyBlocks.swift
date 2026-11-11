import MarkdownUI
import PersonaKit
import SwiftUI

/// A reply's fenced code block as Telegram draws it (#2984): a title bar on top with the fence's language (none when
/// it names none) and Copy, which takes the whole block; the code under it scrolls sideways when it is wide, so a
/// sideways drag on it still goes to the code.
struct ReplyCodeBlock: View {
    var config: CodeBlockConfiguration

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: Theme.Space.s) {
                if let language = config.language?.trimmingCharacters(in: .whitespaces), !language.isEmpty {
                    Text(verbatim: language).font(Theme.Font.labelMedium).foregroundStyle(.secondary).lineLimit(1)
                }
                Spacer(minLength: Theme.Space.s)
                Button { CodeCopy.copy(config.content, said: String(localized: "Code copied")) } label: {
                    HStack(spacing: Theme.Space.xs) {
                        ThemedIcon(Theme.Icon.copy)
                        Text("Copy [Message Menu 63d90d]").lineLimit(1)
                    }
                    .font(Theme.Font.label)
                    .frame(minWidth: Theme.Size.tapTarget, minHeight: Theme.Size.tapTarget)
                    .contentShape(.rect)
                }
                .buttonStyle(.plain)
                .foregroundStyle(.secondary)
                .accessibilityLabel(String(localized: "Copy Code [Message Views fa2611]"))
                .accessibilityIdentifier("code-block-copy")
            }
            .padding(.horizontal, Theme.Space.m)
            // The bar is a label's height; Copy's 44 pt reach past its edges (nothing is clipped, it is drawn over the code).
            .padding(.vertical, -Theme.Space.sm)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(Theme.codeBar, in: UnevenRoundedRectangle(topLeadingRadius: Theme.Radius.control,
                                                                  topTrailingRadius: Theme.Radius.control, style: .continuous))
            .zIndex(1)
            ScrollView(.horizontal, showsIndicators: false) {
                config.label
                    .relativeLineSpacing(.em(0.2))
                    .markdownTextStyle { FontFamilyVariant(.monospaced); FontSize(.em(Theme.Level.code.size / Theme.Level.body.size)) }
                    // The style's font design (RootView's `fontDesign`) won over the monospaced variant: the code was
                    // drawn in the body's font.
                    .fontDesign(.monospaced)
                    .padding(Theme.Space.m)
            }
            .background(Theme.codeBackground, in: UnevenRoundedRectangle(bottomLeadingRadius: Theme.Radius.control,
                                                                         bottomTrailingRadius: Theme.Radius.control, style: .continuous))
        }
    }
}

/// A reply's quote as Telegram draws it (#2984): the accent bar on the left of a faint accent card, a quote mark at its
/// top right. A long one is folded to a few lines, fading out: a tap on it opens it, its chevron folds it again. A
/// quote still being written is never folded (`replyStreaming`): it would fold and unfold as its words came.
struct ReplyQuote: View {
    var config: BlockConfiguration
    @Environment(\.replyStreaming) private var streaming
    @ScaledMetric(relativeTo: .body) private var fold = Theme.Size.quoteFold
    /// The words' room on the right, clear of the quote mark and the chevron: they are drawn in the label's size, so it
    /// grows with them (at the largest text sizes a fixed room let the mark sit on the first line).
    @ScaledMetric(relativeTo: .caption) private var markRoom = Theme.Space.xl
    @State private var height: CGFloat = 0
    @State private var open = false

    /// Long enough to fold: more than a line past what a folded one shows.
    private var folds: Bool { !streaming && height > fold + Theme.Space.xl }
    private var folded: Bool { folds && !open }

    var body: some View {
        config.label
            .markdownTextStyle { ForegroundColor(.secondary) }
            .padding(.leading, Theme.Space.m)
            .padding(.trailing, markRoom)
            .padding(.vertical, Theme.Space.s)
            .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { height = $0 }
            .frame(maxHeight: folded ? fold : nil, alignment: .top)
            .clipped()
            .mask {
                if folded {
                    VStack(spacing: 0) {
                        Color.black
                        LinearGradient(colors: [.black, .clear], startPoint: .top, endPoint: .bottom).frame(height: Theme.Space.xl)
                    }
                } else {
                    Color.black
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.leading, Theme.Size.bar)
            .background(Theme.quoteBackground)
            .overlay(alignment: .leading) { Theme.accent.frame(width: Theme.Size.bar) }
            .clipShape(.rect(cornerRadius: Theme.Radius.badge, style: .continuous))
            .overlay(alignment: .topTrailing) {
                ThemedIcon(Theme.Icon.quoteMark).font(Theme.Font.label).foregroundStyle(Theme.accent).padding(Theme.Space.s)
                    .accessibilityHidden(true)
            }
            .overlay {
                if folded { // the folded quote is one button: it opens
                    Button { withAnimation(Theme.Motion.layout) { open = true } } label: { Color.clear.contentShape(.rect) }
                        .buttonStyle(.plain)
                        .accessibilityLabel(String(localized: "Expand"))
                }
            }
            .overlay(alignment: .bottomTrailing) {
                if folds {
                    Button { withAnimation(Theme.Motion.layout) { open.toggle() } } label: {
                        ThemedIcon(open ? Theme.Icon.collapse : Theme.Icon.expand)
                            .font(Theme.Font.label)
                            .foregroundStyle(Theme.accent)
                            .frame(minWidth: Theme.Size.tapTarget, minHeight: Theme.Size.tapTarget)
                            .contentShape(.rect)
                    }
                    .buttonStyle(.plain)
                    .accessibilityLabel(open ? String(localized: "Collapse") : String(localized: "Expand"))
                    .accessibilityIdentifier("quote-fold")
                }
            }
            .accessibilityElement(children: .contain)
            .accessibilityIdentifier("reply-quote")
    }
}

/// Copying code from a reply: a code block's Copy, a tap on inline code (`CodeCopyURL`, the chat's `openURL`).
enum CodeCopy {
    @MainActor static func copy(_ code: String, said: String) {
        var code = code
        while code.hasSuffix("\n") { code.removeLast() }
        UIPasteboard.general.string = code
        Haptics.success()
        AppModel.shared.toast = Toast(said, style: .success)
    }
}

extension EnvironmentValues {
    /// The reply under it is still being written (`StreamingText`): its quotes do not fold yet.
    @Entry var replyStreaming = false
}
