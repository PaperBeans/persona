import SwiftUI
import PersonaWire

/// Dashboards (#15017): pages a conversation drafts and the owner keeps, which the default manager answers for under the
/// reserved extension id `-pages` (docs/design/DESIGN_SYSTEM.md "Dashboards (#15017)"). The hub lists them as ordinary
/// nav_page places (the index, titled Dashboards, then up to three pinned pages) and draws them as Views, so the extension
/// page hosts show them as they are. What the app adds is the product's own: the header's title never shows a raw page id,
/// a kept page says who keeps it up to date, and Settings › Assistant has the switch.
nonisolated enum DashboardsUI {
    /// The reserved extension id. Like every id that starts with "-", it is drawn over HTTP only (`ReservedExtensionUI`).
    static let id = "-pages"
    /// The index page, the Dashboards place; a kept page's id is `pg_` and 16 hex digits.
    static let index = "index"
    /// The hello feature of a hub that has Dashboards: only then is the switch shown.
    static let feature = "assistant_pages"

    /// Whether `ext` is Dashboards.
    static func owns(_ ext: String) -> Bool { ext == id }

    /// Whether a page of `ext` shows the ownership line: a kept page of Dashboards, not its index.
    static func showsOwnership(_ ext: String, ui: String) -> Bool { owns(ext) && ui != index }

    /// Whether Settings › Assistant shows the switch: the hub announces Dashboards, and the assistant shown is the default
    /// one, the only one that has them.
    static func offered(features: Set<String>, manager: String?) -> Bool {
        features.contains(feature) && (manager ?? "").isEmpty
    }

    /// A Dashboards page's header: the rendered View's title, else the place's title the page was opened with, else
    /// Dashboards. Never the raw page id (`pg_…`), which a deep link or an untitled View would otherwise leave there.
    static func title(view: String?, place: String?, ui: String) -> String {
        for candidate in [view, place] {
            let t = candidate?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
            if !t.isEmpty, t != ui, !isPageID(t) { return t }
        }
        return name
    }

    /// The place's title for `ui` where the hub lists one (the index, a pinned page), for a page opened by a link.
    static func placeTitle(_ ui: String, in places: [ExtensionUIPlace]) -> String? {
        places.first { owns($0.owner) && $0.id == ui }?.title ?? places.first { owns($0.owner) && $0.id == index }?.title
    }

    /// `pg_` and 16 hex digits: a kept page's id.
    static func isPageID(_ s: String) -> Bool {
        s.count == 19 && s.hasPrefix("pg_") && s.dropFirst(3).allSatisfy { "0123456789abcdef".contains($0) }
    }

    static let name = String(localized: "Dashboards")
    /// The line under a kept page's title: the product says who keeps it, the model never writes it.
    static let ownership = String(localized: "Kept up to date by Persona [Dashboards]")
    static let switchTitle = String(localized: "Let Persona make dashboards")
    static let switchFooter = String(localized: "When on, the assistant can draft pages you come back to; tap Keep in the chat and they appear in the sidebar.")
}

extension View {
    /// A kept Dashboards page's ownership line, under its title in the bar: the bar's subtitle from iOS 26, and on iOS 18
    /// (which has none) the title and the line together as the bar's title, as Knowledge's day page draws its chat.
    @ViewBuilder func dashboardsOwnership(_ shown: Bool, title: String) -> some View {
        if shown { modifier(DashboardsOwnershipLine(title: title)) } else { self }
    }
}

private struct DashboardsOwnershipLine: ViewModifier {
    var title: String

    func body(content: Content) -> some View {
        #if os(iOS)
        if #available(iOS 26.0, *) {
            content.navigationSubtitle(DashboardsUI.ownership)
        } else {
            content.toolbar {
                ToolbarItem(placement: .principal) {
                    VStack(spacing: Theme.Space.flush) {
                        Text(title).font(Theme.Font.heading).lineLimit(1)
                        Text(DashboardsUI.ownership).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1)
                    }
                    .accessibilityElement(children: .combine)
                    .accessibilityAddTraits(.isHeader)
                }
            }
        }
        #else
        content.navigationSubtitle(DashboardsUI.ownership)
        #endif
    }
}
