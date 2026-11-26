import SwiftUI
import PersonaWire

/// The server's home card, hosted in the same list and navigation stack as the other Health items.
struct HealthStrengthSection: View {
    var hellos: Int
    var group: String
    var open: (ExtensionPush, String) -> Void
    @State private var page: ExtensionViewModel?

    var body: some View {
        Group {
            if let page { HealthStrengthCard(hellos: hellos, page: page, group: group, open: open) }
            else { Section { ProgressView().frame(maxWidth: .infinity) } header: { Text("Strength Training") } }
        }
        .tabActiveTask(id: true) {
            if page == nil {
                let source = group
                let deps = ExtensionViewModel.Deps(api: { AppModel.shared.api(source) }, connection: { AppModel.shared.socket(source) })
                page = ExtensionViewModel(plugin: HealthIntakeUI.id, ui: HealthIntakeUI.page,
                                          context: .here(size: "compact"), state: HealthStrengthUI.home, deps: deps)
            }
        }
    }
}

private struct HealthStrengthCard: View {
    var hellos: Int
    var page: ExtensionViewModel
    var group: String
    var open: (ExtensionPush, String) -> Void
    @State private var link: LinkTarget?

    var body: some View {
        Section {
            if let view = page.view {
                Button { open(.init(state: HealthStrengthUI.overview, title: view.title), group) } label: {
                    ExtensionViewStack(view: view, model: nil, cards: false, titles: true, link: $link)
                        .foregroundStyle(Color.primary)
                }
                .buttonStyle(.plain)
                .accessibilityIdentifier("health-strength-home")
            } else if let error = page.error {
                LoadProblem(text: error)
                Button("Retry [Components b8784c]") { Task { await page.load() } }.formButton()
            } else { ProgressView().frame(maxWidth: .infinity) }
        } header: { Text(page.view?.title ?? String(localized: "Strength Training")) }
        .pluginLinks($link)
        .pluginRefresh(page)
        .tabActiveTask(id: hellos) { await page.load() }
    }
}

/// Presented by the Health page, outside the list's lazy rows, as intake details are.
struct HealthStrengthSheet: View {
    struct Route: Identifiable {
        var id = UUID()
        var push: ExtensionPush
        var group: String
    }
    var push: ExtensionPush
    var group: String
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            ExtensionPageView(plugin: HealthIntakeUI.id, ui: HealthIntakeUI.page,
                              title: push.title ?? String(localized: "Strength Training"),
                              state: push.state, depth: 2, group: group)
                .toolbar { SheetClose { dismiss() } }
        }
    }
}
