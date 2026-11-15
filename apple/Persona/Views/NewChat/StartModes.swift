import Foundation

/// What New Agent offers for a start's permission mode and thinking effort, by the launcher that will run (#8144).
/// Claude Code's launchers take its five modes, and the efforts its model lists. A provider Paseo runs that is neither
/// Claude Code nor Codex (Grok, Kimi, OpenCode…) takes none of Claude Code's modes: the bridge refuses Plan Only for it
/// (`readonly_unsupported`, so a Plan Only remembered from a Claude Code start failed the next Grok start) and drops
/// Ask First, Auto-Edit and Auto. Only Allow Everything means something there (Paseo starts it with Auto Accept on,
/// docs/integrations/paseo.md "Capabilities"), and only the efforts its model lists. The web and Android follow in
/// their own change.
nonisolated enum StartModes {
    /// A launcher of the Paseo engine (`source` "paseo") whose agent is neither Claude Code nor Codex: the node reports
    /// every such provider with an agent of its own ("paseo").
    static func limited(_ launcher: Launcher?) -> Bool {
        guard let launcher, launcher.source == MachineEngine.paseo.rawValue else { return false }
        let agent = launcher.agent ?? ""
        return !(agent.isEmpty || agent == "claude" || agent == "codex")
    }

    /// The permission modes offered besides the launcher's own (Use Launcher's).
    static func modes(for launcher: Launcher?) -> [SessionModes.Option] {
        guard limited(launcher) else { return SessionModes.permissionModes }
        return SessionModes.permissionModes.filter { $0.id == SessionModes.ModeID.bypass }
    }

    /// The mode a start sends: the pick when the launcher runs it; nil for the launcher's own, or a mode it can't run.
    static func sent(_ mode: String, for launcher: Launcher?) -> String? {
        guard !mode.isEmpty, modes(for: launcher).contains(where: { $0.id == mode }) else { return nil }
        return mode
    }

    /// The efforts offered: the ones the model that will run lists (`known`), every start level while that is not
    /// known. A limited launcher's only from its model's list: none while that is not known.
    static func efforts(known: [String]?, for launcher: Launcher?) -> [SessionModes.Option] {
        guard let known else { return limited(launcher) ? [] : SessionModes.startEffortLevels }
        return SessionModes.startEffortLevels.filter { known.contains($0.id) }
    }
}
