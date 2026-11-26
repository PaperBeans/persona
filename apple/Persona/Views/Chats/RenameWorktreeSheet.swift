import SwiftUI

/// A container has one display name; it never borrows an Agent's tab or conversation title.
struct RenameWorktreeSheet: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    var workspace: HostWorkspace
    var renamed: () async -> Void
    @State private var name = ""
    @State private var busy = false
    @State private var failure: String?

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    WorktreeInput(title: String(localized: "worktree.name", defaultValue: "Worktree name"), text: $name)
                        .accessibilityIdentifier("worktree.rename.name")
                }
                if let failure { Section { Text(failure).foregroundStyle(Theme.danger) } }
            }
            .themedPage()
            .navigationTitle(String(localized: "worktree.rename", defaultValue: "Rename Worktree…"))
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                SheetClose(disabled: busy) { dismiss() }
                SheetConfirm(id: "worktree.rename.save", name: String(localized: "Save"),
                             disabled: busy || name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty) {
                    Task { await save() }
                }
            }
            .disabled(busy)
        }
        .onAppear { name = workspace.title }
        .interactiveDismissDisabled(busy)
    }

    private func save() async {
        guard let api = model.api(workspace.group) else { return }
        busy = true
        defer { busy = false }
        do {
            try await api.nodeAction(machine: workspace.machineID, "worktree/rename",
                                     ["workspace_id": workspace.sourceID, "kind": workspace.source, "name": name])
            await renamed()
            dismiss()
        } catch { failure = error.shown ?? error.localizedDescription }
    }
}

nonisolated enum SessionRenameForm {
    /// Whether the Rename dialog merges into a single "Name" field (#13899, web `RenameDialog` with `use_herdr_tab_name` on).
    static func isUnified(_ session: Session, preferHerdrTabName: Bool) -> Bool {
        session.usesUnifiedHerdrRename(preferTab: preferHerdrTabName)
    }

    /// Only a Claude in a host that renames its tabs (herdr, Tern, Rex: docs/integrations/engines.md) edits both a tab and
    /// the conversation. Other runtimes expose one name.
    static func showsTab(_ session: Session) -> Bool {
        session.paneID != nil && ["herdr", "tern", "rex"].contains(session.hostKind ?? "herdr") && (session.agent ?? "claude") == "claude"
    }

    /// Single-field label used when `use_herdr_tab_name` merges the Rename dialog.
    static var unifiedField: String {
        String(localized: "Name")
    }

    /// Hint text used when `use_herdr_tab_name` merges the Rename dialog (mirrors web `rename_hint_unified`).
    static var unifiedNote: String {
        String(localized: "Updates the herdr tab and, where supported, the CLI session name.")
    }

    /// The tab field and the line under it name the host that renames the tab.
    static func tabField(_ session: Session) -> String {
        switch session.hostKind {
        case "tern": String(localized: "Tab name (Tern tab)")
        case "rex": String(localized: "Tab name (Rex tab)")
        default: String(localized: "Tab name (herdr tab)")
        }
    }

    static func tabNote(_ session: Session) -> String {
        switch session.hostKind {
        case "tern":
            String(localized: "The tab name syncs to Tern. The session name uses Claude's /rename, so claude --resume on the Mac shows it too.")
        case "rex":
            String(localized: "The tab name syncs to Rex. The session name uses Claude's /rename, so claude --resume on the Mac shows it too.")
        default:
            String(localized: "The tab name syncs to herdr. The session name uses Claude's /rename, so claude --resume on the Mac shows it too.")
        }
    }

    static func body(_ session: Session, tab: String, title: String, unified: Bool = false) -> APIClient.RenameBody {
        if unified {
            let next = tab.trimmingCharacters(in: .whitespacesAndNewlines)
            guard !next.isEmpty, let currentTab = session.herdrTabName else {
                return APIClient.RenameBody(tabName: nil, title: nil)
            }
            let cli = (session.agent?.isEmpty == false ? session.agent : nil) ?? "claude"
            let syncTitle = cli == "claude" || cli == "codex"
            if next == currentTab && (!syncTitle || next == session.title) {
                return APIClient.RenameBody(tabName: nil, title: nil)
            }
            return APIClient.RenameBody(tabName: next, title: syncTitle ? next : nil)
        }
        return APIClient.RenameBody(tabName: showsTab(session) && tab != (session.slotName ?? "") ? tab : nil,
                                    title: title != session.title ? title : nil)
    }
}

nonisolated enum SpaceRenameForm {
    /// The line under a space's name says what else it renames: a herdr workspace, or a Tern/Rex session (their spaces
    /// are sessions).
    static func note(_ group: SpaceGroup?) -> String {
        switch group.flatMap(NodeMenu.kind) {
        case "tern": String(localized: "Also renames this session in Tern.")
        case "rex": String(localized: "Also renames this session in Rex.")
        default: String(localized: "Also renames this workspace in herdr.")
        }
    }
}
