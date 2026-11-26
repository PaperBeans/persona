import Foundation

/// What an Agents row read from Unread or Just finished does once it is seen (#12514; docs/design/DESIGN_SYSTEM.md 3.4,
/// "Rows read during a visit"). Kept on this device: Settings › Appearance & Display › Agents List Layout, iPhone and
/// iPad only; elsewhere (no such setting) seen rows leave with their chat, as before.
nonisolated enum SeenRows: String, CaseIterable, Identifiable, Sendable {
    /// It keeps its place, drawn as Seen, until the list is refreshed (`AppModel.seenThisVisit`): pull to refresh, Clear
    /// Seen, a quick filter chosen, another computer, hub or assistant, a new launch. The default since #12514.
    case stay
    /// It moves to Seen once its chat is left, as every row did before #12514 (`AppModel.heldInList`).
    case leave

    static let key = "agents.seenRows"
    /// The one-time notice that says seen rows now stay (the Agents list's `seenRowsNotice`) was closed, or the choice
    /// was made in Settings.
    static let noticedKey = "agents.seenRows.noticed"

    var id: String { rawValue }

    var title: String {
        switch self {
        case .stay: String(localized: "Stay Until Refresh")
        case .leave: String(localized: "Leave When the Chat Closes")
        }
    }

    /// A section header's action that lets the section's seen rows go: "Clear Seen (2)".
    static func clearTitle(_ count: Int) -> String {
        String(localized: "Clear Seen (\(count))")
    }

    /// What VoiceOver says the header's action does.
    static var clearHint: String {
        String(localized: "Takes the agents you have read out of this section.")
    }

    /// The one-time notice over the list when a read row first stays (the Agents list's `seenRowsNotice`): the changed
    /// default, said once, in words that also hold for a new install (nothing "now" about it there).
    static var notice: String {
        String(localized: "Agents you read stay where they were, marked as seen, until you pull down to refresh.")
    }
}

/// This device's choice (`SeenRows`). Only iPhone and iPad have the setting, so the others keep the behavior before #12514.
struct SeenRowsChoice {
    @StoredValue(SeenRows.key) var stored: SeenRows = .stay
    @StoredValue(SeenRows.noticedKey) var noticed = false

    var mode: SeenRows {
        #if os(iOS)
        stored
        #else
        .leave
        #endif
    }
}
