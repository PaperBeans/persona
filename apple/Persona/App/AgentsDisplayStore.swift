import Foundation
import Observation
import PersonaWire

/// Per-hub Agents display settings (`GET`/`PUT /api/v1/agents/display-settings`, #13899):
/// fetches, syncs, and caches `use_herdr_tab_name` in `StateCache` (off/hidden on older hubs that omit
/// `use_herdr_tab_name` or return 404). Mirrors web `web/src/lib/agents-display.ts` and Android `AgentsPreferences`.
@Observable @MainActor
final class AgentsDisplayStore {
    nonisolated struct Cached: Codable, Equatable, Sendable {
        var useHerdrTabName: Bool?
    }

    struct Deps {
        var api: () -> APIClient?
        var loadCache: () -> Cached? = { nil }
        var saveCache: (Cached?) -> Void = { _ in }
    }

    /// Last hub-acknowledged value of `use_herdr_tab_name`:
    /// - `nil`: not yet read or unsupported on this hub (older hub / 404 / omitted field); switch stays hidden and off.
    /// - `false` / `true`: supported and acknowledged by the hub.
    private(set) var useHerdrTabName: Bool? {
        didSet {
            if useHerdrTabName != oldValue {
                onChange?()
            }
        }
    }
    /// True while a PUT is in flight.
    private(set) var saving = false
    /// Last write error shown in Settings with Retry; cleared on next read/write.
    private(set) var error: String?
    /// Last requested value whose write failed, so Retry sends it again.
    private(set) var pendingSave: Bool?

    @ObservationIgnored private let deps: Deps
    @ObservationIgnored var onChange: (() -> Void)?
    /// Incremented on every write so a stale in-flight GET cannot overwrite a newer PUT.
    @ObservationIgnored private var revision = 0
    /// Collapses concurrent GETs into one in-flight task.
    @ObservationIgnored private var readingTask: Task<Void, Never>?

    init(deps: Deps) {
        self.deps = deps
        if let cached = deps.loadCache() {
            self.useHerdrTabName = cached.useHerdrTabName
        }
    }

    static func cacheName(_ group: String) -> String {
        group.isEmpty ? "agents-display" : "agents-display." + group
    }

    static func forget(group: String, in folder: URL? = nil) {
        StateCache.remove(cacheName(group), in: folder)
    }

    /// Refreshes from the hub (called on hello, refresh, and Settings › Agent appear).
    /// Stale reads cannot undo a save; 404/405/501 or omitted `use_herdr_tab_name` marks it unsupported (`nil`);
    /// transient offline/5xx errors preserve the last acknowledged cached value.
    func refresh() async {
        if saving { return }
        if let existing = readingTask {
            await existing.value
            return
        }
        let mine = revision
        let task = Task { @MainActor [weak self] in
            guard let self, let api = self.deps.api() else { return }
            do {
                let settings = try await api.agentsDisplaySettings()
                guard mine == self.revision else { return }
                self.applyAcknowledged(settings.useHerdrTabName)
                if self.pendingSave == nil { self.error = nil }
            } catch let apiError as APIError where apiError.status == 404 || apiError.status == 405 || apiError.status == 501 {
                guard mine == self.revision else { return }
                self.applyAcknowledged(nil)
            } catch {
                // Offline or transient hub failure: keep the last acknowledged value.
            }
        }
        readingTask = task
        await task.value
        if readingTask == task { readingTask = nil }
    }

    /// Saves `use_herdr_tab_name` to the hub (`PUT /api/v1/agents/display-settings`).
    /// Only updates `useHerdrTabName` after the hub acknowledges the write; failures keep the last
    /// acknowledged value and record `error` + `pendingSave` for Retry.
    @discardableResult
    func setUseHerdrTabName(_ enabled: Bool) async -> Bool {
        guard !saving else { return false }
        guard let api = deps.api() else {
            error = String(localized: "Not connected to the hub yet")
            pendingSave = enabled
            return false
        }
        revision &+= 1
        let mine = revision
        saving = true
        pendingSave = enabled
        error = nil
        defer { if mine == revision { saving = false } }
        do {
            let acknowledged = try await api.setAgentsUseHerdrTabName(enabled)
            guard mine == revision else { return false }
            applyAcknowledged(acknowledged.useHerdrTabName)
            pendingSave = nil
            error = nil
            return true
        } catch {
            guard mine == revision else { return false }
            if !error.isCancellation {
                self.error = error.shown ?? error.localizedDescription
            }
            return false
        }
    }

    func retry() async {
        if let pendingSave {
            await setUseHerdrTabName(pendingSave)
        } else {
            await refresh()
        }
    }

    private func applyAcknowledged(_ value: Bool?) {
        if useHerdrTabName != value {
            useHerdrTabName = value
        }
        if let value {
            deps.saveCache(Cached(useHerdrTabName: value))
        } else {
            deps.saveCache(nil)
        }
    }
}
