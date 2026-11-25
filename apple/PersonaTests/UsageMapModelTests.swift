import Foundation
import PersonaWire
import Testing
@testable import Persona

/// The disk usage map's model against a FakeHub (#14576, docs/server/machine-usage-map.md): it polls a scan until it is
/// ready, zooms and goes back, selects, trashes with Undo and Delete Permanently, cleans a catalog cache through the
/// existing cleanup, offloads with the install step, restores, and marks reclaim suggestions.
@MainActor
struct UsageMapModelTests {
    private static let usage = "/api/v1/machines/mac/storage/usage"
    private static let providers = "/api/v1/extensions/-/offload"
    private static let base = "/api/v1/extensions/example/-/offload/cloud"
    private static let gb: Int64 = 1 << 30
    private static let old = Int64(Date.now.timeIntervalSince1970) - 400 * 86_400

    private final class Box { var toasts: [String] = []; var sleeps = 0; var connected = true }

    private static func node(_ name: String, _ bytes: Int64, extra: String = "") -> String {
        #"{"id": "/Users/me/\#(name)", "name": "\#(name)", "path": "/Users/me/\#(name)", "display": "~/\#(name)", "kind": "dir", "category": "personal", "bytes": \#(bytes), "has_children": true, "can_trash": true, "can_offload": true\#(extra)}"#
    }

    /// The overview: a home group with Movies (cold) and Work, a catalog cache, and free space.
    private static func overview(state: String = "ready", movies: Int64 = 40 * gb) -> String {
        """
        {"machine_id": "mac", "state": "\(state)", "path": "", "home": "/Users/me",
         "volume": {"mount": "/", "total_bytes": \(100 * gb), "used_bytes": \(92 * gb), "free_bytes": \(8 * gb)},
         "node": {"id": "group:root", "name": "", "kind": "root", "category": "other", "bytes": \(100 * gb), "has_children": true, "children": [
           {"id": "group:home", "name": "", "key": "home", "kind": "group", "category": "personal", "bytes": \(movies + 30 * gb), "has_children": true, "children": [
             \(node("Movies", movies, extra: #", "used_at": \#(old)"#)), \(node("Work", 30 * gb))]},
           {"id": "/Users/me/Library/Caches/go-build", "name": "go-build", "path": "/Users/me/Library/Caches/go-build", "kind": "dir", "category": "cache",
            "bytes": \(6 * gb), "has_children": true, "can_trash": true, "can_offload": false, "cache_id": "go-build", "cache_in_use": false},
           {"id": "group:free", "name": "", "key": "free", "kind": "free", "category": "free", "bytes": \(8 * gb), "has_children": false}]}}
        """
    }

    private static let movies = """
    {"machine_id": "mac", "state": "ready", "path": "/Users/me/Movies", "node": \(node("Movies", 40 * gb).dropLast()), "children": [
      \(node("Movies/Old", 30 * gb)), \(node("Movies/New", 10 * gb))]}}
    """

    private func make(_ hub: FakeHub, box: Box, reclaim: Bool = false) -> UsageMapModel {
        UsageMapModel(machine: "mac", name: "Mac", reclaim: reclaim, deps: .init(api: { hub.client }, toast: { box.toasts.append($0.message) },
                                                                                   sleep: { _ in }, now: { .now }))
    }

    private func hubWithProvider(installed: Bool = true) -> FakeHub {
        let hub = FakeHub()
        hub.on("GET", Self.providers, json: #"{"providers": [{"extension": "example", "id": "cloud", "name": "Example Cloud", "kinds": ["file", "dir", "app"], "restore": true}]}"#)
        hub.on("GET", Self.base + "/inventory", json: #"""
        {"state": "ready", "at": 1, "total_bytes": 21474836480, "items": [{"id": "pics", "name": "Pictures 2019", "kind": "dir", "bytes": 21474836480,
         "origin_path": "/Users/me/Movies/Pictures 2019", "restorable": true}]}
        """#)
        return hub
    }

    @Test func pollsUntilReadyAndOnlyWhileScanning() async {
        let hub = FakeHub()
        var calls = 0
        hub.on("GET", Self.usage) { _ in
            calls += 1
            return Data(Self.overview(state: calls < 3 ? "scanning" : "ready").utf8)
        }
        let box = Box()
        let m = make(hub, box: box)
        let idle = m.pollKey
        await m.start()
        // The view restarts polling when the key changes: the overview (path "") measuring must change it.
        #expect(m.scanning && m.pollKey != idle)
        await m.poll()
        #expect(!m.scanning && calls == 3)
        await m.poll() // nothing to wait on: returns at once
        #expect(calls == 3)
        #expect(m.providersKnown && m.providers.isEmpty) // an older hub without the route: no provider, quietly
    }

    /// Opened before the hub's client is there (a launch switch, a link at launch): offline first, then the map once the
    /// page starts again with a client.
    @Test func startsAgainOnceTheHubIsThere() async {
        let hub = FakeHub()
        hub.on("GET", Self.usage, json: Self.overview())
        let box = Box()
        box.connected = false
        let m = UsageMapModel(machine: "mac", name: "Mac", reclaim: false,
                              deps: .init(api: { box.connected ? hub.client : nil }, toast: { box.toasts.append($0.message) },
                                          sleep: { _ in }, now: { .now }))
        await m.start()
        #expect(m.problem == .offline && m.overview == nil)
        box.connected = true
        await m.start()
        #expect(m.problem == nil && m.overview != nil)
    }

    @Test func zoomsIntoAFolderAndBack() async throws {
        let hub = FakeHub()
        hub.on("GET", Self.usage) { req in Data((req.query["path"] == "/Users/me/Movies" ? Self.movies : Self.overview()).utf8) }
        let m = make(hub, box: Box())
        await m.start()
        m.setWidth(900)
        await m.sceneSettled()
        let home = try #require(m.scene.local.first { $0.id == "group:home" })
        await m.zoom(into: home)
        #expect(m.breadcrumb.count == 2 && m.localFocus?.id == "group:home")
        await m.sceneSettled()
        let movies = try #require(m.scene.local.first { $0.id == "/Users/me/Movies" })
        await m.zoom(into: movies)
        #expect(m.breadcrumb.count == 3 && m.localFocus?.children?.count == 2)
        #expect(hub.sent("GET", Self.usage).contains { $0.query["path"] == "/Users/me/Movies" })
        await m.sceneSettled()
        #expect(m.scene.local.contains { $0.id == "/Users/me/Movies/Old" })
        #expect(m.zoomOut())
        #expect(m.localFocus?.id == "group:home")
        m.zoomTo(0)
        #expect(m.localFocus?.id == "group:root" && !m.zoomOut())
    }

    @Test func selectionAndWhatIf() async throws {
        let hub = FakeHub()
        hub.on("GET", Self.usage, json: Self.overview())
        let m = make(hub, box: Box())
        await m.start()
        m.setWidth(900)
        await m.sceneSettled()
        let work = try #require(m.scene.local.first { $0.id == "/Users/me/Work" })
        m.toggle(work)
        #expect(m.selection.map(\.id) == ["/Users/me/Work"] && m.selectedIDs.contains(work.id))
        let w = try #require(m.whatIf)
        #expect(w.freeAfter == 38 * Self.gb && w.gained == 30 * Self.gb)
        let free = try #require(m.scene.local.first { $0.node.kind == .free })
        m.toggle(free) // free space is not selectable
        #expect(m.selection.count == 1)
        m.toggle(work)
        #expect(m.selection.isEmpty && m.whatIf == nil)
    }

    @Test func trashThenUndoThenDeletePermanently() async throws {
        let hub = FakeHub()
        hub.on("GET", Self.usage, json: Self.overview())
        hub.on("POST", "/api/v1/machines/mac/storage/trash") { req in
            let body = try JSONSerialization.jsonObject(with: req.body ?? Data()) as? [String: Any]
            #expect(body?["confirm"] as? Bool == true && (body?["paths"] as? [String]) == ["/Users/me/Work"])
            return Data(#"{"machine_id": "mac", "dry_run": false, "freed_bytes": 0, "pending_bytes": 32212254720, "available_before": 1, "available_after": 1, "items": [{"path": "/Users/me/Work", "display": "~/Work", "bytes": 32212254720, "status": "trashed", "trash_id": "t-1"}]}"#.utf8)
        }
        hub.on("POST", "/api/v1/machines/mac/storage/trash/undo", json: #"{"restored": [{"path": "/Users/me/Work"}]}"#)
        hub.on("POST", "/api/v1/machines/mac/storage/trash/purge", json: #"{"freed_bytes": 32212254720, "available_before": 1, "available_after": 2, "items": [{"trash_id": "t-1", "status": "purged"}]}"#)
        let box = Box()
        let m = make(hub, box: box)
        await m.start()
        m.setWidth(900)
        await m.sceneSettled()
        let work = try #require(m.scene.local.first { $0.id == "/Users/me/Work" })
        m.toggle(work)
        m.askTrash(m.selection)
        guard case .trash(let items)? = m.confirmation else { Issue.record("no trash confirmation"); return }
        #expect(items.count == 1)
        await m.confirm(.trash(items))
        #expect(m.trashNotice?.ids == ["t-1"] && m.selection.isEmpty)
        await m.undoTrash()
        #expect(m.trashNotice == nil)
        #expect(hub.sent("POST", "/api/v1/machines/mac/storage/trash/undo").count == 1)

        await m.confirm(.trash(items))
        m.askPurge()
        guard case .purge(let ids, _)? = m.confirmation else { Issue.record("no purge confirmation"); return }
        #expect(ids == ["t-1"])
        await m.confirm(.purge(ids: ids, bytes: 0))
        #expect(m.trashNotice == nil && hub.sent("POST", "/api/v1/machines/mac/storage/trash/purge").count == 1)
        // The overview was asked again after each change.
        #expect(hub.sent("GET", Self.usage).count >= 4)
    }

    @Test func aCatalogCacheIsCleanedNotTrashed() async throws {
        let hub = FakeHub()
        hub.on("GET", Self.usage, json: Self.overview())
        hub.on("POST", "/api/v1/machines/mac/cleanup") { req in
            let body = try JSONSerialization.jsonObject(with: req.body ?? Data()) as? [String: Any]
            #expect((body?["ids"] as? [String]) == ["go-build"])
            return Data(#"{"freed_bytes": 6442450944, "items": [{"id": "go-build", "bytes": 6442450944, "status": "cleaned"}]}"#.utf8)
        }
        let m = make(hub, box: Box())
        await m.start()
        m.setWidth(900)
        await m.sceneSettled()
        let cache = try #require(m.scene.local.first { $0.node.cacheID == "go-build" })
        let s = try #require(m.selected(from: cache))
        #expect(s.cleanable && !s.trashable)
        m.askTrash([s])
        #expect(m.confirmation == nil) // a catalog cache goes through Clean, never Trash
        m.askClean([s])
        guard case .clean(let items)? = m.confirmation else { Issue.record("no clean confirmation"); return }
        await m.confirm(.clean(items))
        #expect(hub.sent("POST", "/api/v1/machines/mac/cleanup").count == 1)
    }

    @Test func offloadAsksToInstallTheToolThenRunsTheJob() async throws {
        let hub = hubWithProvider()
        hub.on("GET", Self.usage, json: Self.overview())
        var runs: [[String: Any]] = []
        hub.on("POST", Self.base + "/run") { req in
            let body = try JSONSerialization.jsonObject(with: req.body ?? Data()) as? [String: Any] ?? [:]
            runs.append(body)
            if body["install"] as? Bool != true {
                let details = try JSON.decoder.decode(JSONValue.self, from: Data(#"{"code": "tool_install_required", "error": "needs its tool", "tool": {"name": "example-cli", "title": "Example CLI", "installed": false, "source": "From the extension's release page, installed in ~/.local/bin."}}"#.utf8))
                throw APIError(status: 409, message: "needs its tool", fields: ["code": "tool_install_required"], details: details)
            }
            return Data(#"{"status": "started", "job": "j-1"}"#.utf8)
        }
        var polls = 0
        hub.on("GET", Self.base + "/jobs/j-1") { _ in
            polls += 1
            return Data((polls < 2 ? #"{"state": "running", "phase": "install", "done_bytes": 0, "total_bytes": 10}"#
                                   : #"{"state": "done", "phase": "cleanup", "done_bytes": 10, "total_bytes": 10, "freed_bytes": 10}"#).utf8)
        }
        let box = Box()
        let m = make(hub, box: box)
        await m.start()
        #expect(m.providers.first?.name == "Example Cloud" && m.cloudFocus?.children?.count == 1)
        m.setWidth(900)
        await m.sceneSettled()
        #expect(m.scene.badges["/Users/me/Movies"] == 1) // its offloaded item, linked to where it came from
        let movies = try #require(m.scene.local.first { $0.id == "/Users/me/Movies" })
        m.toggle(movies)
        await m.sceneSettled()
        #expect(m.cloudFocus?.children?.contains { $0.key == UsageMapSceneBuilder.incomingKey } == true) // its dashed twin
        let p = try #require(m.providers.first)
        m.askOffload(m.selection, to: p)
        guard case .offload(let items, _)? = m.confirmation else { Issue.record("no offload confirmation"); return }
        await m.confirm(.offload(items, p))
        guard case .install(let tool, _, _, _)? = m.confirmation else { Issue.record("no install confirmation"); return }
        #expect(tool.shownName == "Example CLI" && tool.source?.isEmpty == false)
        #expect(runs.count == 1 && runs[0]["confirm"] as? Bool == true && runs[0]["install"] == nil)
        // Declining changes nothing; accepting posts the same run with install.
        await m.confirm(m.confirmation!)
        #expect(runs.count == 2 && runs[1]["install"] as? Bool == true && (runs[1]["paths"] as? [String]) == ["/Users/me/Movies"])
        #expect(m.job?.id == "j-1" && m.selection.isEmpty)
        await m.poll()
        #expect(m.job == nil && polls == 2)
        #expect(hub.sent("GET", Self.base + "/inventory").contains { $0.query["refresh"] == "1" })
    }

    @Test func restoreFromTheCloudSide() async throws {
        let hub = hubWithProvider()
        hub.on("GET", Self.usage, json: Self.overview())
        hub.on("POST", Self.base + "/run") { req in
            let body = try JSONSerialization.jsonObject(with: req.body ?? Data()) as? [String: Any] ?? [:]
            #expect(body["action"] as? String == "restore" && (body["items"] as? [String]) == ["pics"] && body["confirm"] as? Bool == true)
            return Data(#"{"status": "started", "job": "j-2"}"#.utf8)
        }
        let m = make(hub, box: Box())
        await m.start()
        m.setWidth(900)
        await m.sceneSettled()
        let cloud = try #require(m.scene.cloud.first)
        m.toggle(cloud)
        #expect(m.selection.first?.restorable == true && m.whatIf == nil)
        m.askRestore(m.selection)
        guard case .restore(let items, let p)? = m.confirmation else { Issue.record("no restore confirmation"); return }
        await m.confirm(.restore(items, p))
        #expect(m.job?.id == "j-2" && m.job?.action == .restore)
    }

    @Test func refusedOffloadSaysWhyAndStartsNothing() async throws {
        let hub = hubWithProvider()
        hub.on("GET", Self.usage, json: Self.overview())
        hub.on("POST", Self.base + "/run") { _ in
            let details = try JSON.decoder.decode(JSONValue.self, from: Data(#"{"code": "offload_refused", "items": [{"path": "/Users/me/Work", "refusal": "credentials", "reason": "It holds keys or passwords."}]}"#.utf8))
            throw APIError(status: 422, message: "refused", fields: ["code": "offload_refused"], details: details)
        }
        let box = Box()
        let m = make(hub, box: box)
        await m.start()
        m.setWidth(900)
        await m.sceneSettled()
        let work = try #require(m.scene.local.first { $0.id == "/Users/me/Work" })
        m.toggle(work)
        let p = try #require(m.providers.first)
        await m.confirm(.offload(m.selection, p))
        #expect(m.job == nil && box.toasts.count == 1)
        // The rule's code reads in the app's own words, not as "protected".
        #expect(box.toasts.first?.contains(UsageMapText.refusal("credentials")) == true)
    }

    @Test func reclaimMarksSuggestionsAndAChipZoomsAndSelects() async throws {
        let hub = hubWithProvider()
        hub.on("GET", Self.usage, json: Self.overview())
        let m = make(hub, box: Box(), reclaim: true)
        await m.start()
        #expect(m.suggestions.map(\.id) == ["/Users/me/Movies", "/Users/me/Library/Caches/go-build"])
        #expect(m.headline != nil)
        await m.activate(m.suggestions[0])
        #expect(m.localFocus?.id == "group:home" && m.selection.map(\.id) == ["/Users/me/Movies"])
        // The goal selects for the user: the unused cache first.
        m.setGoal(5 * Self.gb)
        #expect(m.selection.map(\.id) == ["/Users/me/Library/Caches/go-build"])
    }

    @Test func anOldNodeOrHubSaysWhatToUpdate() async {
        let hub = FakeHub()
        hub.on("GET", Self.usage) { _ in throw APIError(status: 409, message: "old", fields: ["code": "node_outdated"]) }
        let m = make(hub, box: Box())
        await m.start()
        #expect(m.problem == .nodeOutdated)
        let old = FakeHub() // no route at all: a hub from before the map
        let m2 = make(old, box: Box())
        await m2.start()
        #expect(m2.problem == .hubOutdated)
    }
}
