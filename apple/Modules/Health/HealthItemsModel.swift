import SwiftUI
import PersonaWire

/// Selection, editing and the one-time question share one authoritative revision, scoped to the paired hub.
@Observable final class HealthItemsModel {
    enum SectionID: String, Identifiable { case today, activity, trends, strength; var id: String { rawValue } }
    typealias Deps = AssistantDeps
    private(set) var supported = false
    private(set) var state: HealthItems?
    private(set) var sections: [SectionID] = [.today, .activity, .trends]
    private(set) var todayKinds: [HealthIntake.Kind] = [.water, .protein]
    private(set) var error: String?
    private(set) var busy = false
    private(set) var editing = false
    private(set) var editOrder: [String] = []
    private(set) var selected: Set<String> = []
    private(set) var question = false
    var wantsWater = false
    var wantsProtein = false
    var wantsStrength = false
    private(set) var canShowStrength = false
    private(set) var refresh = 0
    @ObservationIgnored private let deps: Deps
    @ObservationIgnored private var scope: APIClient?
    @ObservationIgnored private var available: [String] = []
    /// Input from this scope's existing intake reading, not a separate persisted cache.
    @ObservationIgnored private var intakeItems: [String]?
    private static let legacyItems = ["water", "protein", "activity", "trends"]
    @ObservationIgnored private var baseline: HealthItems?
    @ObservationIgnored private var reads = 0
    @ObservationIgnored private var pendingRead = false

    init(deps: Deps = .live) { self.deps = deps }

    func load(features: Set<String>, available: [String], intakeItems: [String]? = nil) async {
        let api = deps.api()?.manager(nil)
        if !scopeMatches(api) {
            reads += 1
            scope = api
            state = nil
            self.intakeItems = nil
            editing = false
            busy = false
            error = nil
            wantsWater = false
            wantsProtein = false
            wantsStrength = false
        }
        self.available = available.filter { $0 != "strength" || features.contains(HealthStrengthUI.feature) }
        canShowStrength = self.available.contains("strength")
        if editing {
            // A module turned off while the editor was open: its slots become hidden and must be retained on save.
            editOrder.removeAll { !self.available.contains($0) }
            selected.formIntersection(editOrder)
        }
        if let intakeItems { self.intakeItems = intakeItems }
        supported = features.contains(HealthItems.feature)
        project()
        guard supported else {
            reads += 1
            state = nil
            editing = false
            busy = false
            pendingRead = false
            project()
            return
        }
        guard let api else { return }
        guard !busy else { pendingRead = true; return }
        reads += 1
        let read = reads
        do {
            let value = try await api.healthItems()
            guard read == reads, current(api), !Task.isCancelled else { return }
            state = value
            if !editing { error = nil }
            project()
        } catch {
            guard read == reads, current(api), !Task.isCancelled else { return }
            self.error = error.shown ?? error.localizedDescription
        }
    }

    private func scopeMatches(_ api: APIClient?) -> Bool {
        guard let scope, let api else { return scope == nil && api == nil }
        return scope.sameSettingsScope(as: api)
    }
    private func current(_ api: APIClient) -> Bool {
        supported && scopeMatches(api) && api.sameSettingsScope(as: deps.api()?.manager(nil))
    }

    /// Intake can finish while the items request is pending or failed. Its list is useful only before the first
    /// successful items response; saved state, including an empty list, always wins.
    func useIntakeItems(_ items: [String]?) {
        guard let scope, current(scope), state == nil else { return }
        intakeItems = items
        project()
    }

    private func project() {
        let ids = scope == nil ? [] : (supported ? (state?.items ?? intakeItems ?? Self.legacyItems) : Self.legacyItems)
        todayKinds = ids.compactMap(HealthIntake.Kind.init(rawValue:))
        var result: [SectionID] = []
        for id in ids where available.contains(id) {
            let section: SectionID? = id == "water" || id == "protein" ? .today : SectionID(rawValue: id)
            if let section, !result.contains(section) { result.append(section) }
        }
        sections = result
        question = supported && state?.saved == false && !ids.contains("water") && !ids.contains("protein") && !(canShowStrength && ids.contains("strength"))
    }

    func beginEditing() {
        guard let state, !busy else { return }
        baseline = state
        let editable = Set(available).intersection(state.catalog.map(\.id))
        editOrder = state.items.filter { editable.contains($0) }
        editOrder += available.filter { editable.contains($0) && !editOrder.contains($0) }
        selected = Set(state.items).intersection(editable)
        error = nil
        editing = true
    }
    func closeEditor() {
        guard !busy else { return }
        editing = false
        error = nil
    }
    func setAdded(_ id: String, _ added: Bool) {
        guard !busy, editOrder.contains(id) else { return }
        if added { selected.insert(id) } else { selected.remove(id) }
    }
    func move(from offsets: IndexSet, to destination: Int) {
        guard !busy else { return }
        editOrder.move(fromOffsets: offsets, toOffset: destination)
    }
    func moveUp(_ id: String) {
        guard let index = editOrder.firstIndex(of: id), index > 0 else { return }
        move(from: IndexSet(integer: index), to: index - 1)
    }
    func saveEditor() async {
        guard let baseline else { return }
        let ordered = editOrder.filter { selected.contains($0) }
        let items = HealthItems.merging(ordered, into: baseline.items, editable: Set(editOrder))
        await save(items, baseline: baseline, closesEditor: true)
    }
    func answerQuestion(add: Bool) async {
        guard question, let state else { return }
        var ids = state.items
        if add {
            if wantsWater { ids.append("water") }
            if wantsProtein { ids.append("protein") }
            if canShowStrength && wantsStrength { ids.append("strength") }
        }
        await save(ids, baseline: state, closesEditor: false)
    }
    private func save(_ items: [String], baseline: HealthItems, closesEditor: Bool) async {
        guard !busy, let api = scope, current(api) else { return }
        busy = true
        error = nil
        reads += 1
        defer {
            if current(api) {
                busy = false
                if pendingRead { pendingRead = false; refresh += 1 }
            }
        }
        do {
            let value = try await api.saveHealthItems(items, revision: baseline.revision)
            guard current(api) else { return }
            state = value
            if closesEditor { editing = false }
            project()
            deps.intakeChanged()
            refresh += 1
        } catch {
            guard current(api) else { return }
            if let conflict = error as? APIError, conflict.status == 409,
               let details = conflict.details,
               let data = try? JSON.encoder.encode(details),
               let value = try? JSON.decoder.decode(HealthItems.self, from: data) {
                state = value
                project()
                // The conflict is the current state, never a retry that silently overwrites it.
                busy = false
                if closesEditor { beginEditing() }
                self.error = String(localized: "Changed elsewhere. Updated to the latest health items.")
            } else {
                self.error = error.shown ?? error.localizedDescription
            }
        }
    }
}
