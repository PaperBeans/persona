import Foundation
import Observation
import PersonaWire
import SwiftUI

/// One computer's disk usage map (#14576, docs/server/machine-usage-map.md "The map", "Low-disk guidance"): the overview
/// and every folder opened (polled once a second while the node is still measuring, only while the page is in front),
/// the zoom trail of each side, the selection and its "what if", the tint, the offload providers and what they already
/// hold, and the changes: Move to Trash with Undo and Delete Permanently, Clean Cache through the existing cleanup,
/// Offload and Restore through the `disk_offload` extension point with the install confirmation. The scene the view
/// draws is laid out off the main actor and cached (`UsageMapSceneBuilder`; docs/apple/performance.md P4, P8, P9, P11).
/// Tests run it against a `FakeHub` (UsageMapModelTests).
@Observable final class UsageMapModel {
    struct Deps {
        var api: () -> APIClient?
        var toast: (Toast) -> Void
        var sleep: @Sendable (Duration) async throws -> Void = { try await Task.sleep(for: $0) }
        var now: () -> Date = { .now }

        static func live(group: String) -> Deps {
            let model = AppModel.shared
            return Deps(api: { model.api(group) }, toast: { model.toast = $0 })
        }
    }

    enum Tint: Hashable, Sendable { case kind, age }

    /// Why the map is not there, in the words the page shows.
    enum Problem: Equatable, Sendable {
        case hubOutdated, nodeOutdated, offline, forbidden, deadline
        case failed(String)

        static func of(_ error: Error) -> Problem {
            guard let e = error as? APIError else { return .failed(error.localizedDescription) }
            if e.status == 409, e.code == "node_outdated" { return .nodeOutdated }
            if e.status == 404, e.code == nil { return .hubOutdated }
            if e.status == 403 { return .forbidden }
            if e.status == 503 || e.code == "machine_offline" { return .offline }
            if e.status == 504 || e.code == "deadline" { return .deadline }
            return .failed(e.localizedDescription)
        }

        var text: String {
            switch self {
            case .hubOutdated: String(localized: "Update the hub to see what's using space on this computer.")
            case .nodeOutdated: String(localized: "Update Persona on this computer to see what's using its space.")
            case .offline: String(localized: "This computer is offline. The map comes back when it reconnects.")
            case .forbidden: String(localized: "Only a fully trusted device can look at and change this computer's disk.")
            case .deadline: String(localized: "The computer didn't answer in time. Try again.")
            case .failed(let s): s
            }
        }

        var retryable: Bool {
            switch self {
            case .offline, .deadline, .failed: true
            default: false
            }
        }
    }

    /// One step of a side's zoom trail.
    struct Step: Hashable, Sendable {
        var id: String
        var node: StorageUsageNode
    }

    /// One selected box: what the tray lists and the actions take.
    struct Selected: Identifiable, Hashable, Sendable {
        var id: String
        var side: TreemapBox.Side
        var name: String
        var display: String?
        var path: String?
        var bytes: Int64
        var kind: StorageUsageKind
        var category: StorageUsageCategory = .other
        var usedAt: Int64?
        var canTrash: Bool
        var canOffload: Bool
        var refusal: String?
        var cacheID: String?
        var cacheInUse: Bool
        /// On the cloud side: whose item, and which.
        var providerID: String?
        var itemID: String?
        var restorable: Bool

        var trashable: Bool { side == .local && canTrash && cacheID == nil && path != nil }
        var cleanable: Bool { side == .local && cacheID != nil && !cacheInUse }
    }

    enum Confirmation: Identifiable, Sendable {
        case trash([Selected])
        case purge(ids: [String], bytes: Int64)
        case clean([Selected])
        case offload([Selected], OffloadProvider)
        /// The provider's tool is not on the computer: a second confirmation names it, the computer and where it comes from.
        case install(OffloadTool, OffloadProvider, OffloadRunRequest, [Selected])
        case restore([Selected], OffloadProvider)

        var id: String {
            switch self {
            case .trash: "trash"
            case .purge: "purge"
            case .clean: "clean"
            case .offload: "offload"
            case .install: "install"
            case .restore: "restore"
            }
        }
    }

    /// What the last Move to Trash did, for its notice (Undo, Delete Permanently).
    struct TrashNotice: Equatable, Sendable {
        var ids: [String]
        var paths: [String]
        var bytes: Int64
        var count: Int
        var refused: Int
    }

    struct JobState: Equatable, Sendable {
        var provider: OffloadProvider
        var id: String
        var action: OffloadRunRequest.Action
        var job: OffloadJob?
        var paths: [String]
        var count: Int
        var bytes: Int64
    }

    let machine: String
    let reclaim: Bool
    /// The computer's name, for the root box and the confirmations.
    var machineName: String

    private(set) var overview: StorageUsageReport?
    private(set) var folders: [String: StorageUsageReport] = [:]
    private(set) var trail: [Step] = []
    private(set) var cloudTrail: [Step] = []
    private(set) var problem: Problem?
    /// A folder opened that failed to load (the map stays as it was).
    private(set) var loadingPaths: Set<String> = []
    private(set) var scanningPaths: Set<String> = []
    private(set) var providers: [OffloadProvider] = []
    /// The providers were asked (an older hub has none: the cloud side says so quietly).
    private(set) var providersKnown = false
    private(set) var inventories: [String: OffloadInventory] = [:]
    private(set) var selection: [Selected] = []
    private(set) var selectedIDs: Set<String> = []
    var tint: Tint = .kind
    /// The box under the pointer or the focus; drives the detail line and the ribbons.
    private(set) var hovered: String?
    private(set) var suggestions: [TreemapLayout.Candidate] = []
    var confirmation: Confirmation?
    private(set) var trashNotice: TrashNotice?
    private(set) var job: JobState?
    private(set) var busy = false
    /// The free-up goal in bytes (0: off) and the most it can reach.
    private(set) var goal: Int64 = 0
    private(set) var goalMost: Int64 = 0
    /// The local and cloud focus as resolved for the scene.
    private(set) var localFocus: StorageUsageNode?
    private(set) var cloudFocus: StorageUsageNode?
    private(set) var scene: UsageMapScene = .empty
    /// Changes whenever what the scene is made of does.
    private(set) var revision = 0
    /// Each zoom: where the new level grows from, in the side's own coordinates (0…1), for the one animated progress.
    private(set) var zoom: (serial: Int, from: CGRect, side: TreemapBox.Side) = (0, .zero, .local)

    @ObservationIgnored private let deps: Deps
    @ObservationIgnored private var width: CGFloat = 0
    @ObservationIgnored private var sceneCache: [UsageMapSceneBuilder.Key: UsageMapScene] = [:]
    @ObservationIgnored private var sceneOrder: [UsageMapSceneBuilder.Key] = []
    @ObservationIgnored private var sceneTask: Task<Void, Never>?
    @ObservationIgnored private var folderOrder: [String] = []
    @ObservationIgnored private var cloudItems: [String: (OffloadProvider, OffloadItem)] = [:]
    @ObservationIgnored private var cloudOrigins: [String: String] = [:]
    @ObservationIgnored private var cloudRoot: StorageUsageNode?
    @ObservationIgnored private var started = false

    /// Folders kept in memory, least recently opened out first (P11).
    static let folderCap = 64
    static let sceneCap = 12

    init(machine: String, name: String, reclaim: Bool = false, deps: Deps) {
        self.machine = machine
        self.machineName = name
        self.reclaim = reclaim
        self.deps = deps
    }

    // MARK: Loading and polling

    /// The page came to the front: the overview (at once in reclaim mode, as anywhere), then the providers.
    func start() async {
        if overview == nil {
            if problem == .offline { problem = nil } // started again once the hub's client is there
            await load(path: "")
        }
        if !providersKnown { await loadProviders() }
        started = true
    }

    /// Asks the node for one folder (or the overview, ""); a slow one answers "scanning" with what it has so far.
    func load(path: String, rescan: Bool = false) async {
        guard let api = deps.api() else { problem = .offline; return }
        loadingPaths.insert(path)
        defer { loadingPaths.remove(path) }
        do {
            let r = try await api.storageUsage(machine: machine, path: path, rescan: rescan)
            if path.isEmpty {
                overview = r
                problem = nil
            } else {
                folders[path] = r
                folderOrder.removeAll { $0 == path }
                folderOrder.append(path)
                while folderOrder.count > Self.folderCap { folders.removeValue(forKey: folderOrder.removeFirst()) }
            }
            if r.state == .scanning { scanningPaths.insert(path) } else { scanningPaths.remove(path) }
            rebuild()
        } catch is CancellationError {
            return
        } catch {
            scanningPaths.remove(path)
            if path.isEmpty || overview == nil {
                problem = .of(error)
            } else {
                deps.toast(Toast(Problem.of(error).text, style: .error))
            }
        }
    }

    /// The node is still measuring what the local side shows.
    var scanning: Bool {
        scanningPaths.contains(trail.last(where: { $0.node.path != nil })?.node.path ?? "")
    }

    /// What polling waits on; the view restarts its polling task when it changes.
    var pollKey: String {
        // Each path in brackets: the overview's path is "", which joined alone would read as no path at all.
        let paths = scanningPaths.filter(visible).sorted().map { "[\($0)]" }.joined()
        return paths + "#" + (job.map { $0.id + ($0.job?.finished == true ? "done" : "") } ?? "")
    }

    /// Asks again once a second while a shown folder is being measured or a job runs; returns when neither does.
    func poll() async {
        while !Task.isCancelled {
            let paths = scanningPaths.filter(visible)
            let running = job.map { $0.job?.finished != true } ?? false
            guard !paths.isEmpty || running else { return }
            do { try await deps.sleep(Theme.UsageMap.poll) } catch { return }
            for p in paths.sorted() { await load(path: p) }
            if running { await pollJob() }
        }
    }

    /// The overview and the folders on the trail are on screen; others are not polled.
    private func visible(_ path: String) -> Bool {
        path.isEmpty || trail.contains { $0.node.path == path }
    }

    func retry() async {
        problem = nil
        await load(path: "")
        if !providersKnown { await loadProviders() }
    }

    func loadProviders() async {
        guard let api = deps.api() else { return }
        do {
            providers = try await api.offloadProviders(machine: machine)
        } catch {
            providers = [] // an older hub has no such route: the cloud side says nothing is offloaded
        }
        providersKnown = true
        for p in providers { await loadInventory(p) }
        rebuild()
    }

    func loadInventory(_ p: OffloadProvider, refresh: Bool = false) async {
        guard let api = deps.api() else { return }
        do {
            inventories[p.id] = try await api.offloadInventory(p, machine: machine, refresh: refresh)
        } catch {
            inventories[p.id] = OffloadInventory(state: .unavailable, detail: Problem.of(error).text, items: [])
        }
        rebuild()
    }

    // MARK: Layout

    /// The map's width changed: lay it out for that width.
    func setWidth(_ w: CGFloat) {
        guard abs(w - width) >= 1 else { return }
        width = w
        schedule()
    }

    /// Waits for the scene being laid out (tests, and the first zoom).
    func sceneSettled() async { await sceneTask?.value }

    private func rebuild() {
        revision += 1
        if let root = overview?.node {
            var cur = hydrate(root)
            var steps: [Step] = []
            for s in trail {
                guard let next = cur.children?.first(where: { $0.id == s.id }) ?? (s.node.id == s.id ? s.node : nil) else { break }
                cur = hydrate(next)
                steps.append(Step(id: s.id, node: cur))
            }
            if steps.count != trail.count { trail = steps }
            localFocus = cur
        } else {
            localFocus = nil
        }
        // The dashed twins of what is selected and a provider would take: the "what if" on the cloud side.
        let incoming = selection.filter(offloadable).map {
            StorageUsageNode(id: $0.id, name: $0.name, display: $0.display, kind: $0.kind, category: $0.category, bytes: $0.bytes, usedAt: $0.usedAt)
        }
        let built = UsageMapSceneBuilder.cloudTree(providers.compactMap { p in inventories[p.id].map { (p, $0) } }, incoming: incoming,
                                                   otherName: String(localized: "Other [Usage Map]"))
        cloudRoot = built.root
        cloudItems = built.items
        cloudOrigins = built.origins
        var cur = built.root
        var steps: [Step] = []
        for s in cloudTrail {
            guard let next = cur.children?.first(where: { $0.id == s.id }) else { break }
            cur = next
            steps.append(Step(id: s.id, node: next))
        }
        if steps.count != cloudTrail.count { cloudTrail = steps }
        cloudFocus = cur
        if reclaim, let root = overview?.node {
            suggestions = TreemapLayout.suggestReclaim(in: deepHydrate(root, depth: 3), offloadable: { [providers] n in
                n.canOffload && providers.contains { $0.takes(n.kind) }
            }, now: deps.now())
        }
        goalMost = goalCandidates().reduce(0) { $0 + $1.node.bytes }
        schedule()
    }

    /// A node with what the node measured of it since (its own folder answer), and its children's.
    private func hydrate(_ n: StorageUsageNode) -> StorageUsageNode {
        var out = n
        if let p = n.path, let r = folders[p] {
            out = r.node
            if out.name.isEmpty { out.name = n.name }
            out.key = out.key ?? n.key
            out.category = n.category == .other ? out.category : n.category
        }
        out.children = out.children?.map { c in
            guard let p = c.path, let r = folders[p] else { return c }
            var k = r.node
            if k.name.isEmpty { k.name = c.name }
            k.category = c.category
            return k
        }
        return out
    }

    private func deepHydrate(_ n: StorageUsageNode, depth: Int) -> StorageUsageNode {
        var out = hydrate(n)
        guard depth > 0 else { return out }
        out.children = out.children?.map { deepHydrate($0, depth: depth - 1) }
        return out
    }

    private func schedule() {
        guard width > 0, let local = localFocus else {
            scene = .empty
            return
        }
        let cloud = cloudFocus ?? StorageUsageNode(id: UsageMapSceneBuilder.cloudRootID, name: "", kind: .root, category: .other, bytes: 0)
        let localTotal = trail.isEmpty ? max(overview?.volume?.totalBytes ?? 0, local.bytes) : local.bytes
        let input = UsageMapSceneBuilder.Input(local: local, cloud: cloud, localTotal: localTotal, cloudTotal: cloud.bytes, width: width,
                                               origins: cloudOrigins, revision: revision, machineName: machineName)
        let key = UsageMapSceneBuilder.key(input)
        if let hit = sceneCache[key] {
            scene = hit
            return
        }
        sceneTask?.cancel()
        sceneTask = Task { [weak self] in
            let built = await Task.detached(priority: .userInitiated) { UsageMapSceneBuilder.build(input) }.value
            guard !Task.isCancelled, let self else { return }
            self.sceneCache[key] = built
            self.sceneOrder.append(key)
            while self.sceneOrder.count > Self.sceneCap { self.sceneCache.removeValue(forKey: self.sceneOrder.removeFirst()) }
            self.scene = built
        }
    }

    // MARK: Zoom

    /// Opens a box: it becomes the whole side; a folder not measured yet starts its scan and fills in.
    func zoom(into box: TreemapBox) async {
        guard box.zoomable else { return }
        let side = box.side == .local ? scene.split.local : scene.split.cloud
        let from = side.width > 0 && side.height > 0
            ? CGRect(x: (box.rect.minX - side.minX) / side.width, y: (box.rect.minY - side.minY) / side.height,
                     width: box.rect.width / side.width, height: box.rect.height / side.height)
            : .zero
        let boxes = box.side == .local ? scene.local : scene.cloud
        var chain: [String] = []
        var cur: TreemapBox? = box
        let focusID = box.side == .local ? localFocus?.id : cloudFocus?.id
        while let b = cur, b.id != focusID {
            chain.insert(b.id, at: 0)
            cur = b.parentID.flatMap { pid in boxes.first { $0.id == pid } }
        }
        var node = box.side == .local ? localFocus : cloudFocus
        var steps: [Step] = []
        for id in chain {
            guard let next = node?.children?.first(where: { $0.id == id }) else { break }
            steps.append(Step(id: id, node: next))
            node = next
        }
        guard !steps.isEmpty else { return }
        zoom = (zoom.serial + 1, from, box.side)
        if box.side == .local { trail += steps } else { cloudTrail += steps }
        hovered = nil
        rebuild()
        if box.side == .local, let path = steps.last?.node.path, folders[path] == nil {
            await load(path: path)
        }
    }

    /// Goes up one level of a side (Escape, the back swipe); false at the top.
    @discardableResult func zoomOut(_ side: TreemapBox.Side = .local) -> Bool {
        if side == .local {
            guard !trail.isEmpty else { return cloudTrail.isEmpty ? false : zoomOut(.cloud) }
            trail.removeLast()
        } else {
            guard !cloudTrail.isEmpty else { return false }
            cloudTrail.removeLast()
        }
        zoom = (zoom.serial + 1, .zero, side)
        rebuild()
        return true
    }

    /// A breadcrumb step: `count` steps kept (0: the top).
    func zoomTo(_ count: Int, side: TreemapBox.Side = .local) {
        if side == .local {
            guard count < trail.count else { return }
            trail = Array(trail.prefix(count))
        } else {
            guard count < cloudTrail.count else { return }
            cloudTrail = Array(cloudTrail.prefix(count))
        }
        zoom = (zoom.serial + 1, .zero, side)
        rebuild()
    }

    var breadcrumb: [String] {
        [machineName] + trail.map { UsageMapText.name($0.node, machine: machineName) }
    }

    // MARK: Hover and details

    func hover(_ id: String?) {
        guard hovered != id else { return }
        hovered = id
    }

    /// The cloud box a local one links to, and back: the pair lit by a hover.
    func partners(of id: String) -> Set<String> {
        if let local = scene.links[id] { return [local] }
        return Set(scene.links.filter { $0.value == id }.map(\.key))
    }

    /// "Name · 41 GB · 17% · Changed 3 months ago · why an action is not offered", for the detail line and VoiceOver.
    func detail(_ box: TreemapBox) -> String {
        box.detail.isEmpty ? UsageMapSceneBuilder.detail(box, base: box.side == .local ? localFocus?.bytes ?? 0 : cloudFocus?.bytes ?? 0, now: deps.now()) : box.detail
    }

    func label(_ box: TreemapBox) -> String {
        if !box.title.isEmpty { return box.title }
        let parent = box.parentID.flatMap { pid in (box.side == .local ? scene.local : scene.cloud).first { $0.id == pid }?.node }
        return UsageMapText.fullName(box.node, parent: parent, machine: machineName)
    }

    /// The VoiceOver label: name, size, share, last changed, kind, and selected.
    func accessibilityLabel(_ box: TreemapBox) -> String {
        var s = detail(box) + " · " + UsageMapText.category(box.node.category)
        if box.side == .cloud { s += " · " + String(localized: "Offloaded") }
        if box.node.key == UsageMapSceneBuilder.incomingKey { s += " · " + String(localized: "Will be offloaded") }
        if selectedIDs.contains(box.id) { s += " · " + String(localized: "Selected") }
        return s
    }

    // MARK: Selection

    func selected(from box: TreemapBox) -> Selected? {
        guard box.node.kind != .free, box.node.kind != .root, box.node.key != UsageMapSceneBuilder.incomingKey else { return nil }
        if box.side == .cloud {
            guard let (p, it) = cloudItems[box.id] else { return nil }
            return Selected(id: box.id, side: .cloud, name: it.name, display: it.originPath, path: nil, bytes: it.bytes, kind: box.node.kind,
                            category: box.node.category, usedAt: it.usedAt, canTrash: false, canOffload: false, cacheInUse: false, providerID: p.id, itemID: it.id,
                            restorable: it.restorable && p.restore)
        }
        guard box.node.path != nil else { return nil }
        let n = box.node
        return Selected(id: box.id, side: .local, name: label(box), display: n.display ?? n.path, path: n.path, bytes: n.bytes, kind: n.kind,
                        category: n.category, usedAt: n.usedAt, canTrash: n.canTrash, canOffload: n.canOffload, refusal: n.refusal, cacheID: n.cacheID,
                        cacheInUse: n.cacheInUse ?? false, restorable: false)
    }

    func selectable(_ box: TreemapBox) -> Bool { selected(from: box) != nil }

    func toggle(_ box: TreemapBox) {
        if selectedIDs.contains(box.id) { deselect(box.id); return }
        guard let s = selected(from: box) else { return }
        // A side at a time: offload and trash act on local items, restore on cloud ones.
        if let first = selection.first, first.side != s.side { clearSelection() }
        selection.append(s)
        selectedIDs.insert(s.id)
        rebuild()
    }

    func deselect(_ id: String) {
        selection.removeAll { $0.id == id }
        selectedIDs.remove(id)
        rebuild()
    }

    func clearSelection() {
        guard !selection.isEmpty else { return }
        selection = []
        selectedIDs = []
        goal = 0
        rebuild()
    }

    /// The selection's bytes, each byte once (a folder and something in it).
    var selectedBytes: Int64 { TreemapLayout.distinctBytes(selection.map { ($0.path, $0.bytes) }) }

    var whatIf: TreemapLayout.WhatIf? {
        guard let v = overview?.volume, !selection.isEmpty, selection.first?.side == .local else { return nil }
        return TreemapLayout.whatIf(volume: v, selected: selectedBytes)
    }

    // MARK: Reclaim

    /// "24 GB free of 245 GB. Freeing the 3 marked items returns about 31 GB."
    var headline: String? {
        guard let v = overview?.volume else { return nil }
        let free = String(localized: "\(UsageMapText.size(v.freeBytes)) free of \(UsageMapText.size(v.totalBytes)).")
        guard !suggestions.isEmpty else { return free }
        let sum = suggestions.reduce(0) { $0 + $1.node.bytes }
        return free + " " + String(localized: "Freeing the \(suggestions.count) marked items returns about \(UsageMapText.size(sum)).")
    }

    /// A suggestion chip: zooms to its folder and selects it, which shows the "what if".
    func activate(_ c: TreemapLayout.Candidate) async {
        guard let root = overview?.node else { return }
        var steps: [Step] = []
        var cur = hydrate(root)
        for id in c.trail.dropFirst() {
            guard let next = cur.children?.first(where: { $0.id == id }) else { break }
            cur = hydrate(next)
            steps.append(Step(id: id, node: cur))
        }
        trail = steps
        zoom = (zoom.serial + 1, .zero, .local)
        rebuild()
        if !selectedIDs.contains(c.node.id) {
            let box = TreemapBox(id: c.node.id, node: c.node, parentID: nil, rect: .zero, depth: 1, framed: false, big: false,
                                 zoomable: false, side: .local)
            if let s = selected(from: box) {
                selection.removeAll { $0.side == .cloud }
                selection.append(s)
                selectedIDs = Set(selection.map(\.id))
            }
        }
        hovered = c.node.id
        rebuild()
    }

    func isSuggested(_ id: String) -> Bool { suggestions.contains { $0.id == id } }

    // MARK: The free-up goal

    private func goalCandidates() -> [TreemapLayout.Candidate] {
        guard let root = overview?.node else { return [] }
        return TreemapLayout.candidates(in: deepHydrate(root, depth: 3), offloadable: { [providers] n in
            n.canOffload && providers.contains { $0.takes(n.kind) }
        }, now: deps.now(), coldOnly: false)
    }

    /// Selects for the user until `bytes` would be free: unused caches first, then by size × age. It only selects.
    func setGoal(_ bytes: Int64) {
        goal = max(0, bytes)
        let picks = TreemapLayout.pickForGoal(goalCandidates(), goal: goal, now: deps.now())
        selection = picks.compactMap { c in
            selected(from: TreemapBox(id: c.node.id, node: c.node, parentID: nil, rect: .zero, depth: 1, framed: false, big: false,
                                      zoomable: false, side: .local))
        }
        selectedIDs = Set(selection.map(\.id))
        rebuild()
    }

    // MARK: Actions

    func provider(_ id: String?) -> OffloadProvider? { providers.first { $0.id == id } }

    func offloadable(_ s: Selected) -> Bool { s.side == .local && s.canOffload && providers.contains { $0.takes(s.kind) } }

    /// The providers that take every item.
    func offloadProviders(for items: [Selected]) -> [OffloadProvider] {
        providers.filter { p in !items.isEmpty && items.allSatisfy { $0.side == .local && $0.canOffload && p.takes($0.kind) } }
    }

    func askTrash(_ items: [Selected]) {
        let ok = items.filter(\.trashable)
        guard !ok.isEmpty else { return }
        confirmation = .trash(ok)
    }

    func askClean(_ items: [Selected]) {
        let ok = items.filter(\.cleanable)
        guard !ok.isEmpty else { return }
        confirmation = .clean(ok)
    }

    func askOffload(_ items: [Selected], to p: OffloadProvider) {
        let ok = items.filter { $0.side == .local && $0.canOffload && p.takes($0.kind) }
        guard !ok.isEmpty else { return }
        confirmation = .offload(ok, p)
    }

    func askRestore(_ items: [Selected]) {
        let ok = items.filter { $0.side == .cloud && $0.restorable }
        guard let p = provider(ok.first?.providerID) else { return }
        confirmation = .restore(ok.filter { $0.providerID == p.id }, p)
    }

    func askPurge() {
        guard let n = trashNotice, !n.ids.isEmpty else { return }
        confirmation = .purge(ids: n.ids, bytes: n.bytes)
    }

    /// The confirmation was accepted.
    func confirm(_ c: Confirmation) async {
        confirmation = nil
        switch c {
        case .trash(let items): await trash(items)
        case .purge(let ids, _): await purge(ids)
        case .clean(let items): await clean(items)
        case .offload(let items, let p): await run(p, OffloadRunRequest(machine: machine, action: .offload, paths: items.compactMap(\.path), confirm: true), items: items)
        case .install(_, let p, var body, let items):
            body.install = true
            await run(p, body, items: items)
        case .restore(let items, let p): await run(p, OffloadRunRequest(machine: machine, action: .restore, items: items.compactMap(\.itemID), confirm: true), items: items)
        }
    }

    private func trash(_ items: [Selected]) async {
        guard let api = deps.api() else { return }
        busy = true
        defer { busy = false }
        do {
            let r = try await api.storageTrash(machine: machine, paths: items.compactMap(\.path))
            let done = r.items.filter { $0.status == .trashed }
            let refused = r.items.filter { $0.status == .refused || $0.status == .failed }
            trashNotice = TrashNotice(ids: r.trashIDs, paths: done.map(\.path), bytes: done.reduce(0) { $0 + $1.bytes }, count: done.count,
                                      refused: refused.count)
            let gone = Set(done.map(\.path))
            selection.removeAll { $0.path.map(gone.contains) ?? false }
            selectedIDs = Set(selection.map(\.id))
            if let first = refused.first {
                deps.toast(Toast(String(localized: "\(first.display) wasn't moved: \(first.refusal.map(UsageMapText.refusal) ?? first.error ?? "")"), style: .error))
            }
            await refresh(after: done.map(\.path))
        } catch {
            deps.toast(Toast(Problem.of(error).text, style: .error))
        }
    }

    /// Undo: puts the items back where they were.
    func undoTrash() async {
        guard let n = trashNotice, let api = deps.api() else { return }
        busy = true
        defer { busy = false }
        do {
            let r = try await api.storageUntrash(machine: machine, trashIDs: n.ids)
            trashNotice = nil
            deps.toast(Toast(String(localized: "Put back \(r.restored.count) items"), style: .success))
            await refresh(after: n.paths)
        } catch {
            deps.toast(Toast(Problem.of(error).text, style: .error))
        }
    }

    func dismissTrashNotice() { trashNotice = nil }

    private func purge(_ ids: [String]) async {
        guard let api = deps.api() else { return }
        busy = true
        defer { busy = false }
        do {
            let r = try await api.storagePurge(machine: machine, trashIDs: ids)
            trashNotice = nil
            if r.unsupported {
                deps.toast(Toast(String(localized: "This computer can't delete from its trash here. Empty it on the computer."), style: .info))
            } else {
                deps.toast(Toast(String(localized: "Freed \(UsageMapText.size(r.freedBytes))"), style: .success))
            }
            await load(path: "")
        } catch {
            deps.toast(Toast(Problem.of(error).text, style: .error))
        }
    }

    private func clean(_ items: [Selected]) async {
        guard let api = deps.api() else { return }
        busy = true
        defer { busy = false }
        do {
            let r = try await api.machineCleanup(machine: machine, ids: items.compactMap(\.cacheID))
            deps.toast(Toast(String(localized: "Freed \(UsageMapText.size(r.freedBytes)) on the disk"), style: .success))
            let ids = Set(items.map(\.id))
            selection.removeAll { ids.contains($0.id) }
            selectedIDs = Set(selection.map(\.id))
            await refresh(after: items.compactMap(\.path))
        } catch {
            deps.toast(Toast(Problem.of(error).text, style: .error))
        }
    }

    private func run(_ p: OffloadProvider, _ body: OffloadRunRequest, items: [Selected]) async {
        guard let api = deps.api() else { return }
        busy = true
        defer { busy = false }
        do {
            let r = try await api.offloadRun(p, body)
            if let id = r.job {
                job = JobState(provider: p, id: id, action: body.action, job: nil,
                               paths: body.action == .offload ? items.compactMap(\.path) : items.compactMap(\.display),
                               count: items.count, bytes: items.reduce(0) { $0 + $1.bytes })
                let ids = Set(items.map(\.id))
                selection.removeAll { ids.contains($0.id) }
                selectedIDs = Set(selection.map(\.id))
                rebuild()
            } else if r.status == "needs_install", let tool = r.tool {
                confirmation = .install(tool, p, body, items)
            }
        } catch let e as APIError where e.status == 409 && e.code == "tool_install_required" {
            let tool = Self.decode(OffloadTool.self, e.details?["tool"]) ?? OffloadTool(name: p.name)
            confirmation = .install(tool, p, body, items)
        } catch let e as APIError where e.status == 422 && e.code == "offload_refused" {
            let refused = Self.decode([OffloadRefusal].self, e.details?["items"]) ?? []
            // A protection rule's code reads in the app's own words; a provider's refusal only has its sentence.
            let why = refused.first.map { r in
                if let code = r.refusal, !code.isEmpty { return UsageMapText.refusal(code) }
                return r.reason.isEmpty ? UsageMapText.refusal(nil) : r.reason
            } ?? e.localizedDescription
            deps.toast(Toast(String(localized: "\(p.name) can't take \(refused.count) of these: \(why)"), style: .error))
        } catch {
            deps.toast(Toast(Problem.of(error).text, style: .error))
        }
    }

    private static func decode<T: Decodable>(_ t: T.Type, _ v: JSONValue?) -> T? {
        guard let v, let data = try? JSON.encoder.encode(v) else { return nil }
        return try? JSON.decoder.decode(T.self, from: data)
    }

    private func pollJob() async {
        guard let j = job, let api = deps.api() else { return }
        do {
            let state = try await api.offloadJob(j.provider, machine: machine, job: j.id)
            job?.job = state
            guard state.finished else { return }
            switch state.state {
            case .done:
                let freed = state.freedBytes ?? 0
                deps.toast(Toast(j.action == .offload
                    ? String(localized: "Offloaded \(j.count) items, freed \(UsageMapText.size(freed))")
                    : String(localized: "Restored \(j.count) items"), style: .success))
            case .canceled: deps.toast(Toast(String(localized: "Canceled. Nothing more was moved."), style: .info))
            default: deps.toast(Toast(state.error ?? String(localized: "\(j.provider.name) couldn't finish."), style: .error))
            }
            job = nil
            await loadInventory(j.provider, refresh: true)
            await refresh(after: j.action == .offload ? j.paths : [])
        } catch {
            job = nil
            deps.toast(Toast(Problem.of(error).text, style: .error))
        }
    }

    func cancelJob() async {
        guard let j = job, let api = deps.api() else { return }
        try? await api.offloadCancel(j.provider, machine: machine, job: j.id)
    }

    /// After a change only what it touched is asked again: each item's folder with `rescan`, the folders on the trail
    /// as they are (the node updates them in place) and the overview; the zoom and the focus stay.
    private func refresh(after paths: [String]) async {
        let parents = Set(paths.map { ($0 as NSString).deletingLastPathComponent })
        for p in parents.sorted() where folders[p] != nil { await load(path: p, rescan: true) }
        for s in trail { if let p = s.node.path, !parents.contains(p), folders[p] != nil { await load(path: p) } }
        await load(path: "")
    }

    // MARK: Keyboard

    enum Direction: Sendable { case left, right, up, down }

    /// The box the arrow key goes to: the nearest whose center lies that way, either side of the map.
    func neighbor(of id: String?, _ d: Direction) -> String? {
        let boxes = scene.targets
        guard let id, let from = boxes.first(where: { $0.id == id }) else { return boxes.first?.id }
        let c = CGPoint(x: from.rect.midX, y: from.rect.midY)
        var best: (String, CGFloat)?
        for b in boxes where b.id != id {
            let dx = b.rect.midX - c.x, dy = b.rect.midY - c.y
            let along: CGFloat, across: CGFloat
            switch d {
            case .left: (along, across) = (-dx, abs(dy))
            case .right: (along, across) = (dx, abs(dy))
            case .up: (along, across) = (-dy, abs(dx))
            case .down: (along, across) = (dy, abs(dx))
            }
            guard along > 0 else { continue }
            let score = along + 2 * across
            if best == nil || score < best!.1 { best = (b.id, score) }
        }
        return best?.0
    }
}
