import AppKit
import Foundation

/// `persona://link?…` reaching the Mac app (#4198): both this app and Persona Link register `persona` / `persona`, and
/// Launch Services picks one of them. The link is for Persona Link on the computer joining the hub, never a pairing
/// of this app: it goes on to Persona Link when that is installed, else the app says to install it.
nonisolated enum PersonaLinkHandoff {
    /// How to build and install Persona Link (no package yet, docs/assistant/onboarding.md 4.2).
    static let installDoc = URL(string: "https://github.com/nwverse/persona/blob/main/docs/operations/persona-link-install.md")!

    /// The executable Persona Link's bundle keeps (docs/operations/rename.md), whatever bundle id it was built with
    /// (`mac.link_bundle_id`, docs/operations/config.md): how it is told apart from this app among the scheme's handlers.
    static let executable = "PersonaLink"
    static let bundleName = "Persona Link.app"

    /// The bundle is Persona Link.
    static func isPersonaLink(_ info: [String: Any]?) -> Bool {
        guard let name = info?["CFBundleExecutable"] as? String else { return false }
        return name == executable || name == LegacyNames.capitalizedProduct + "Link"
    }

    /// Persona Link's bundle: a registered handler of the link first, else the places `mac-link.sh install` puts it
    /// (an app nobody opened yet may not be registered for the scheme).
    static func app(for url: URL, handlers: [URL], fallbacks: [URL], info: (URL) -> [String: Any]?) -> URL? {
        (handlers + fallbacks).first { isPersonaLink(info($0)) }
    }

    static var fallbacks: [URL] {
        let names = [bundleName, LegacyNames.capitalizedProduct + " Link.app"]
        let roots = [URL(fileURLWithPath: "/Applications"),
                     FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("Applications")]
        return names.flatMap { name in roots.map { $0.appendingPathComponent(name) } }
    }

    @MainActor static func installedApp(for url: URL) -> URL? {
        app(for: url, handlers: NSWorkspace.shared.urlsForApplications(toOpen: url), fallbacks: fallbacks) {
            Bundle(url: $0)?.infoDictionary
        }
    }
}

extension AppModel {
    /// A computer's pairing link: on to Persona Link, or how to get it. Nothing is paired here.
    func openComputerLink(_ url: URL) {
        guard let app = PersonaLinkHandoff.installedApp(for: url) else {
            toast = Toast(String(localized: "This pairing link is for Persona Link. Install Persona Link first"), style: .info,
                          action: Toast.Action(title: String(localized: "Install guide")) {
                              NSWorkspace.shared.open(PersonaLinkHandoff.installDoc)
                          })
            return
        }
        NSWorkspace.shared.open([url], withApplicationAt: app, configuration: NSWorkspace.OpenConfiguration()) { @Sendable _, error in
            guard let error else { return }
            let reason = error.localizedDescription
            Task { @MainActor in
                AppModel.shared.toast = Toast(String(localized: "Couldn't open Persona Link: \(reason)"), style: .error)
            }
        }
    }
}
