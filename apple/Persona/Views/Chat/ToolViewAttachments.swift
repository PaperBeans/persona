import PersonaWire
import SwiftUI

/// Declarative `ExtensionView` attachments emitted out-of-band by a turn's tool calls (`ToolResult.tool_views` and
/// `Fold.tool_views`): extracted in turn order and rendered between the work summary and the final reply (#14730).
/// Model runtimes never receive these views (docs/SUPER_RULES.md, rule 2).
nonisolated enum ToolViews {
    static func of(_ work: [WorkItem]) -> [ExtensionView] {
        var out: [ExtensionView] = []
        var seen = Set<ExtensionView>()
        func add(_ view: ExtensionView) {
            if seen.insert(view).inserted { out.append(view) }
        }
        for item in work {
            switch item {
            case .step(.call(let c)): c.toolViews.forEach(add)
            case .step(.folded(let f)): (f.fold?.toolViews ?? []).map(\.view).forEach(add)
            default: continue
            }
        }
        return out
    }

    static func hasDrawableContent(_ view: ExtensionView) -> Bool {
        view.sections.contains { section in
            section.items.contains { !ExtensionViewStack.leftOut($0, html: false) }
        }
    }
}

/// Renders one or more out-of-band `ExtensionView` cards inline under a turn's work line.
struct ToolViewAttachmentsRow: View, Equatable {
    var views: [ExtensionView]
    @Binding var link: LinkTarget?

    nonisolated static func == (a: Self, b: Self) -> Bool { a.views == b.views }

    private var drawable: [ExtensionView] {
        views.filter(ToolViews.hasDrawableContent)
    }

    var body: some View {
        let items = drawable
        if !items.isEmpty {
            VStack(alignment: .leading, spacing: Theme.Space.s) {
                ForEach(Array(items.enumerated()), id: \.element) { index, view in
                    VStack(alignment: .leading, spacing: Theme.Space.s) {
                        if let title = view.title, !title.isEmpty {
                            Text(title)
                                .font(Theme.Font.heading)
                                .foregroundStyle(Color.primary)
                                .accessibilityAddTraits(.isHeader)
                        }
                        ExtensionViewStack(view: view, model: nil, cards: false, titles: true, link: $link)
                            .environment(\.pluginUICompact, true)
                            .environment(\.extensionCardInset, Theme.Space.m)
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .card(padding: Theme.Space.m)
                    .accessibilityElement(children: .contain)
                    .accessibilityIdentifier("tool-view-card-\(index)")
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
    }
}
