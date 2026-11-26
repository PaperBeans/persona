import PersonaModuleAPI
import PersonaWire
import SwiftUI

/// Whether this hub offers the Lark bot's entry points (#15995): the hub's `module.lark` flag, read by the one rule every
/// module follows (`HubModules.allows`). Off only when the hub publishes module flags (`modules`) and not this one; an
/// older hub and a hub never heard from offer it.
///
/// The hub's Lark module is what connects the bot. With it off the bot never connects, so its entry points go: the
/// Lark Bot row in Settings › Connectors, the bot's channel in Settings › Channels, and both pages (the switch,
/// credentials, pairing, the test message, the daily sync). What the user already has stays (the owner's rule, #13430).
///
/// This is not `LarkFeatures`: those are what the installed Lark *extension* can do (`lark`, `lark_moments`, …: the
/// Messages page, the Inbox's Lark chats, the readers, Settings › Connectors › Lark, all on `/api/v1/lark/…`, which the
/// hub keeps serving with the module off). Neither reads the other.
nonisolated enum LarkBotEntry: Hashable, Sendable {
    case offered
    case moduleOff

    /// The bot's channel in `GET /api/v1/channels` (docs/extensions/channels.md).
    static let channelID = "feishu"

    /// The hub's answer as the entry.
    init(hub: HubModules) {
        self = hub.allows(LarkModule.descriptor) ? .offered : .moduleOff
    }

    var isOffered: Bool { self == .offered }

    /// The channels Settings lists and counts: the hub's list, without the bot's while the module is off. Offered, the
    /// very list.
    func listing(_ channels: [HubChannel]) -> [HubChannel] {
        isOffered ? channels : channels.filter { $0.id != Self.channelID }
    }

    /// Whether a channel's page is the bot's, and so replaced by the one line while the module is off.
    func hides(channel id: String) -> Bool { !isOffered && id == Self.channelID }
}

/// Lark as the hub's module flags see it. It is not a packaged module on Apple (the bot's settings are in
/// `apple/Persona/Views/Settings`), so it is not in `Modules.builtIn`; it has the module's id, and so its flag.
enum LarkModule {
    /// `lark`, gated by `module.lark`. It registers nothing, so it names no slot.
    nonisolated static let descriptor = ModuleDescriptor(id: "lark", hubGated: true, slots: [])

    /// The entry of the hub the app follows. Observed through the module registry: a view that reads it draws again
    /// when a hello changes the hub's answer (at most once per hello; a set lookup, cheap in `body`:
    /// docs/apple/performance.md P4, P5).
    @MainActor static var botEntry: LarkBotEntry { LarkBotEntry(hub: ModuleState.shared.registry.hub) }

    /// The one line a page of the bot's shows while the module is off.
    static var offLine: String {
        String(localized: "Lark is turned off on the hub machine, so the Lark bot can't be set up or changed here. Everything you already have stays.")
    }
}

/// A settings page that belongs to a hub module which is off, reached by a link or a saved place: the page's own title
/// and one calm line. It asks the hub nothing, so there is no error and no Retry that cannot succeed.
struct ModuleOffPage: View {
    var title: String
    var line: String

    var body: some View {
        SettingsForm {
            Section {
                Text(line).foregroundStyle(.secondary)
                    .accessibilityIdentifier("module-off-line")
            }
        }
        .themedPage()
        .detailTitle(title)
        .navigationBarTitleDisplayMode(.inline)
    }
}

/// A page of the Lark bot's: itself while the hub offers the bot, else the one line. It reads the entry where it
/// draws, so the page behind it does not depend on the module registry.
struct LarkBotGate<Content: View>: View {
    @ViewBuilder var content: () -> Content

    var body: some View {
        if LarkModule.botEntry.isOffered {
            content()
        } else {
            ModuleOffPage(title: String(localized: "Lark Bot"), line: LarkModule.offLine)
        }
    }
}
