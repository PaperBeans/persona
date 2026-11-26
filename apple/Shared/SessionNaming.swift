import Foundation

extension Session {
    /// The live herdr tab label for an Agent in herdr or Herdr App (matches server `Session.HerdrTabName`,
    /// web `herdrTabName`, and Android `Session.herdrTabName`): nil for assistant chats, sessions without a
    /// tab/slot association, non-herdr hosts, or blank labels.
    nonisolated var herdrTabName: String? {
        guard !assistant else { return nil }
        let hasSlot = !(slotID?.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ?? true)
        let hasTab = !(tabID?.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ?? true)
        guard hasSlot || hasTab else { return nil }
        let rawSpaceKind = spaceKind?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        let rawHost = host?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        let kind: String? = !rawSpaceKind.isEmpty ? rawSpaceKind : (!rawHost.isEmpty ? rawHost : hostKind)
        if let kind, kind != "herdr" && kind != "herdr-core" { return nil }
        let label = slotName?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        return label.isEmpty ? nil : label
    }

    /// The primary herdr tab name when `preferTab` is enabled.
    /// When `preferTab` is false, returns nil so default-off rendering remains unchanged (#13899).
    nonisolated func displayTabName(preferTab: Bool) -> String? {
        preferTab ? herdrTabName : nil
    }

    /// Main session name: uses the herdr tab name when `preferTab` is enabled and available, else `displayTitle`.
    nonisolated func displayTitle(preferTab: Bool) -> String {
        displayTabName(preferTab: preferTab) ?? displayTitle
    }

    /// Secondary CLI session title when `preferTab` promotes the herdr tab name to the main title and the CLI title
    /// differs (web `sessionTitleDetail`). Nil when `preferTab` is false, no herdr tab name applies, or titles match.
    nonisolated func displayTitleDetail(preferTab: Bool) -> String? {
        guard let tab = displayTabName(preferTab: preferTab) else { return nil }
        let cli = title.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !cli.isEmpty, cli != tab else { return nil }
        return cli
    }

    /// Whether the Rename dialog merges into a single "Name" field (web `RenameDialog` with `use_herdr_tab_name` on).
    nonisolated func usesUnifiedHerdrRename(preferTab: Bool) -> Bool {
        preferTab && herdrTabName != nil
    }

    /// Place components for `ChatView.devSubtitle` (full line and narrow fallback), moving the CLI session title to the
    /// detail line and suppressing duplicate tab labels when `preferTab` promotes the herdr tab name (#13899).
    nonisolated func chatSubtitleParts(machine: String, tabShown: Bool, preferTab: Bool) -> (full: [String], short: [String]) {
        let primaryTab = displayTabName(preferTab: preferTab)
        let detail = displayTitleDetail(preferTab: preferTab)
        let hideTab = tabShown || primaryTab != nil
        let place = [spaceName, hideTab ? nil : slotLabel].compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: " › ")
        let full = [detail, machine, place].compactMap { $0 }.filter { !$0.isEmpty }
        let short = [detail, place.isEmpty ? machine : place].compactMap { $0 }.filter { !$0.isEmpty }
        return (full, short)
    }

    /// Main row title in full-window Agent search (`AssistantFullSearch`).
    nonisolated func fullSearchTitle(preferTab: Bool) -> String {
        displayTabName(preferTab: preferTab) ?? (title.isEmpty ? URL(fileURLWithPath: cwd).lastPathComponent : title)
    }

    /// Secondary row detail in full-window Agent search (`AssistantFullSearch`), prepending a differing CLI title when
    /// `preferTab` promotes the herdr tab name.
    nonisolated func fullSearchSubtitle(stateLabel: String, machine: String?, preferTab: Bool) -> String {
        [displayTitleDetail(preferTab: preferTab), stateLabel, machine]
            .compactMap { $0 }
            .filter { !$0.isEmpty }
            .joined(separator: " · ")
    }
}
