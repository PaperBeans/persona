import PersonaWire
import SwiftUI

/// Under the map (docs/server/machine-usage-map.md "The map"): the last trash's notice with Undo and
/// Delete Permanently, an offload's progress, the "what if" line, the selection as rows (the map's non-visual
/// equivalent: every box action is here too) with its actions, and the free-up goal.
struct UsageMapTray: View {
    var model: UsageMapModel

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.m) {
            if let n = model.trashNotice { UsageTrashNotice(model: model, notice: n) }
            if let j = model.job { UsageJobRow(model: model, job: j) }
            if !model.selection.isEmpty { UsageSelectionCard(model: model) }
            if model.goalMost >= Int64(Theme.UsageMap.goalStep) { UsageGoalRow(model: model) }
        }
    }
}

private struct UsageTrashNotice: View {
    var model: UsageMapModel
    var notice: UsageMapModel.TrashNotice

    var body: some View {
        NoticeBanner(text: String(localized: "Moved \(notice.count) items (\(UsageMapText.size(notice.bytes))) to the Trash. The space returns when the Trash is emptied."),
                     tone: .neutral, symbol: Theme.Icon.delete, dismiss: { model.dismissTrashNotice() }) {
            HStack(spacing: Theme.Space.m) {
                Button { Task { await model.undoTrash() } } label: { Text("Undo").lineLimit(1) }
                    .themedButton()
                    .accessibilityIdentifier("usage.trash.undo")
                Button(role: .destructive) { model.askPurge() } label: { Text("Delete Permanently…").lineLimit(1).minimumScaleFactor(0.8) }
                    .themedButton()
                    .disabled(notice.ids.isEmpty)
                    .accessibilityIdentifier("usage.trash.purge")
            }
            .disabled(model.busy)
        }
        .accessibilityIdentifier("usage.trash.notice")
    }
}

private struct UsageJobRow: View {
    var model: UsageMapModel
    var job: UsageMapModel.JobState

    var body: some View {
        let j = job.job
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            HStack(spacing: Theme.Space.s) {
                ThemedIcon(job.action == .offload ? Theme.Icon.cloudOffload : Theme.Icon.cloudRestore).foregroundStyle(Theme.accent)
                Text(job.action == .offload ? String(localized: "Offloading \(job.count) items to \(job.provider.name)")
                                            : String(localized: "Restoring \(job.count) items from \(job.provider.name)"))
                    .font(Theme.Font.secondaryStrong).lineLimit(1).minimumScaleFactor(0.8)
                Spacer(minLength: 0)
                Button { Task { await model.cancelJob() } } label: { Text("Cancel").lineLimit(1) }
                    .themedButton()
            }
            ProgressView(value: j?.fraction ?? 0)
            Text(j.map { "\(UsageMapText.phase($0.phase)) · \(UsageMapText.size($0.doneBytes)) / \(UsageMapText.size($0.totalBytes))" }
                 ?? UsageMapText.phase(nil))
                .font(Theme.Font.label.monospacedDigit()).foregroundStyle(.secondary).lineLimit(1)
        }
        .card(padding: Theme.Space.m)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("usage.job")
    }
}

private struct UsageSelectionCard: View {
    var model: UsageMapModel

    var body: some View {
        let items = model.selection
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            if let w = model.whatIf {
                Text(String(localized: "Free after: \(UsageMapText.size(w.freeAfter)) (+\(UsageMapText.size(w.gained))) · \(UsageMapText.percent(w.usedBefore)) → \(UsageMapText.percent(w.usedAfter))"))
                    .font(Theme.Font.secondaryStrong.monospacedDigit()).lineLimit(2)
                    .accessibilityIdentifier("usage.whatif")
            } else {
                Text(String(localized: "\(items.count) selected · \(UsageMapText.size(model.selectedBytes))"))
                    .font(Theme.Font.secondaryStrong.monospacedDigit()).lineLimit(1)
            }
            ForEach(items) { s in row(s) }
            actions(items)
        }
        .card(padding: Theme.Space.m)
        // A container: its rows and buttons keep their own identifiers.
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("usage.tray")
    }

    private func row(_ s: UsageMapModel.Selected) -> some View {
        HStack(spacing: Theme.Space.s) {
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                Text(s.name).font(Theme.Font.secondary).lineLimit(1)
                if let d = s.display { Text(d).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle) }
            }
            Spacer(minLength: Theme.Space.s)
            Text(UsageMapText.size(s.bytes)).font(Theme.Font.label.monospacedDigit()).foregroundStyle(.secondary).lineLimit(1)
            Button { model.deselect(s.id) } label: { ThemedIcon(Theme.Icon.clear).foregroundStyle(.tertiary) }
                .buttonStyle(.plain)
                .accessibilityLabel(String(localized: "Remove \(s.name) from the selection"))
        }
        .accessibilityElement(children: .combine)
    }

    @ViewBuilder private func actions(_ items: [UsageMapModel.Selected]) -> some View {
        let trash = items.filter(\.trashable)
        let clean = items.filter(\.cleanable)
        let restore = items.filter { $0.side == .cloud && $0.restorable }
        let providers = model.providers.filter { p in items.contains { $0.side == .local && $0.canOffload && p.takes($0.kind) } }
        ViewThatFits(in: .horizontal) {
            HStack(spacing: Theme.Space.s) { buttons(trash: trash, clean: clean, restore: restore, providers: providers, items: items) }
            VStack(alignment: .leading, spacing: Theme.Space.s) { buttons(trash: trash, clean: clean, restore: restore, providers: providers, items: items) }
        }
        .disabled(model.busy || model.job != nil)
    }

    @ViewBuilder private func buttons(trash: [UsageMapModel.Selected], clean: [UsageMapModel.Selected], restore: [UsageMapModel.Selected],
                                      providers: [OffloadProvider], items: [UsageMapModel.Selected]) -> some View {
        if providers.count == 1, let p = providers.first {
            Button { model.askOffload(items, to: p) } label: { Text("Offload \(items.filter { $0.canOffload && p.takes($0.kind) }.count)…").lineLimit(1) }
                .themedButton(prominent: true)
                .accessibilityIdentifier("usage.tray.offload")
        } else if providers.count > 1 {
            Menu {
                ForEach(providers) { p in Button(String(localized: "Offload to \(p.name)…")) { model.askOffload(items, to: p) } }
            } label: { Text("Offload \(items.filter(\.canOffload).count)…").lineLimit(1) }
                .themedButton(prominent: true)
        }
        if !restore.isEmpty {
            Button { model.askRestore(restore) } label: { Text("Restore \(restore.count)…").lineLimit(1) }
                .themedButton(prominent: true)
                .accessibilityIdentifier("usage.tray.restore")
        }
        if !clean.isEmpty {
            Button { model.askClean(clean) } label: { Text("Clean \(clean.count)…").lineLimit(1) }
                .themedButton()
        }
        if !trash.isEmpty {
            Button(role: .destructive) { model.askTrash(trash) } label: { Text("Move to Trash…").lineLimit(1) }
                .themedButton()
                .accessibilityIdentifier("usage.tray.trash")
        }
        Button { model.clearSelection() } label: { Text("Clear Selection").lineLimit(1) }
            .themedButton()
    }
}

/// "Free up 40 GB": selects for the user until the goal is met; it only selects.
private struct UsageGoalRow: View {
    var model: UsageMapModel
    @State private var value: Double = 0

    var body: some View {
        let step = Theme.UsageMap.goalStep
        let most = (Double(model.goalMost) / step).rounded(.down) * step
        VStack(alignment: .leading, spacing: Theme.Space.xs) {
            Text(value > 0 ? String(localized: "Free Up \(UsageMapText.size(Int64(value)))") : String(localized: "Free Up…"))
                .font(Theme.Font.secondaryStrong).lineLimit(1)
            Slider(value: $value, in: 0...max(step, most), step: step) { editing in
                if !editing { model.setGoal(Int64(value)) }
            }
            .accessibilityLabel(String(localized: "Free-up goal"))
            .accessibilityValue(UsageMapText.size(Int64(value)))
            .accessibilityIdentifier("usage.goal")
            Text("Selects unused caches first, then the biggest items unchanged the longest. Nothing changes until you confirm.")
                .font(Theme.Font.label).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
        }
        .onChange(of: model.goal) { _, g in if Double(g) != value { value = Double(g) } }
    }
}

// MARK: - Confirmations

/// Every change asks first, naming what it touches (principle 3): Move to Trash lists the items, their paths and the
/// total and says the space returns when the Trash is emptied; Delete Permanently has its own; Clean Cache uses the
/// disk sheet's words; Offload names the provider, then the install confirmation names the tool, the computer and where
/// the tool comes from. Declining changes nothing.
struct UsageConfirmations: ViewModifier {
    var model: UsageMapModel
    var machineName: String

    func body(content: Content) -> some View {
        let shown = Binding(get: { model.confirmation != nil }, set: { if !$0 { model.confirmation = nil } })
        let c = model.confirmation
        content.accessibleConfirmationDialog(c.map(title) ?? "", isPresented: shown, titleVisibility: .visible, hasCancel: true, presenting: c) { c in
            Button(confirmLabel(c), role: destructive(c) ? .destructive : nil) { Task { await model.confirm(c) } }
                .accessibilityIdentifier("usage.confirm")
            Button("Cancel", role: .cancel) { model.confirmation = nil }
        } message: { c in
            Text(message(c))
        }
    }

    private func destructive(_ c: UsageMapModel.Confirmation) -> Bool {
        switch c {
        case .trash, .purge: true
        default: false
        }
    }

    private func total(_ items: [UsageMapModel.Selected]) -> String {
        UsageMapText.size(TreemapLayout.distinctBytes(items.map { ($0.path, $0.bytes) }))
    }

    /// Every item with its place and size; a long list ends with how many more.
    private func list(_ items: [UsageMapModel.Selected]) -> String {
        let shown = items.prefix(8).map { "\($0.name) — \($0.display ?? "") (\(UsageMapText.size($0.bytes)))" }
        let more = items.count > 8 ? [String(localized: "and \(items.count - 8) more")] : []
        return (shown + more).joined(separator: "\n")
    }

    private func title(_ c: UsageMapModel.Confirmation) -> String {
        switch c {
        case .trash(let items): String(localized: "Move \(items.count) Items to the Trash?")
        case .purge(let ids, _): String(localized: "Delete \(ids.count) Items Permanently?")
        case .clean: String(localized: "Clean These Caches?")
        case .offload(let items, let p): String(localized: "Offload \(items.count) Items to \(p.name)?")
        case .install(let tool, _, _, _): String(localized: "Install \(tool.shownName) on \(machineName)?")
        case .restore(let items, let p): String(localized: "Restore \(items.count) Items from \(p.name)?")
        }
    }

    private func confirmLabel(_ c: UsageMapModel.Confirmation) -> String {
        switch c {
        case .trash: String(localized: "Move to Trash")
        case .purge: String(localized: "Delete Permanently")
        case .clean(let items): String(localized: "Clean \(total(items))")
        case .offload: String(localized: "Offload")
        case .install: String(localized: "Install and Offload")
        case .restore: String(localized: "Restore [Usage Map]")
        }
    }

    private func message(_ c: UsageMapModel.Confirmation) -> String {
        switch c {
        case .trash(let items):
            return list(items) + "\n\n" + String(localized: "Total \(total(items)) on \(machineName). The space returns when the Trash is emptied; until then you can put them back.")
        case .purge(_, let bytes):
            return String(localized: "This frees about \(UsageMapText.size(bytes)) on \(machineName) and can't be undone.")
        case .clean(let items):
            return String(localized: "These folders on \(machineName) will be emptied, \(total(items)) in total:") + "\n" + list(items) + "\n\n"
                + String(localized: "Anything in use when the cleanup runs is skipped. These caches are rebuilt when needed.")
        case .offload(let items, let p):
            return list(items) + "\n\n" + String(localized: "Total \(total(items)). \(p.name) uploads them and frees the space on \(machineName); they stay on the map's cloud side and can be brought back.")
        case .install(let tool, let p, _, _):
            let source = tool.source.map { "\n\n" + $0 } ?? ""
            return String(localized: "\(p.name) needs \(tool.shownName) on \(machineName) to offload. Nothing has been installed or moved yet.") + source
        case .restore(let items, let p):
            return list(items) + "\n\n" + String(localized: "\(p.name) brings back \(total(items)) to where they were on \(machineName). Make sure it has the room.")
        }
    }
}
