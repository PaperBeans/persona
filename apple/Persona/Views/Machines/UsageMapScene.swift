import CoreGraphics
import Foundation
import PersonaWire

/// Everything the map draws for one size and one state, worked out off the main actor (`UsageMapModel.scene(for:)`,
/// docs/apple/performance.md P4, P8): both sides' boxes on one scale, the cloud badges, which cloud box links to which
/// local one, and the boxes big enough to take a tap.
nonisolated struct UsageMapScene: Sendable {
    var split: TreemapLayout.Split
    var local: [TreemapBox]
    var cloud: [TreemapBox]
    /// A local box id → how many offloaded items it holds (on the deepest drawn box only).
    var badges: [String: Int]
    /// A cloud box id → the local box it came from (the deepest drawn ancestor of its origin).
    var links: [String: String]
    /// The boxes that get their own hit target, deepest last so they sit on top.
    var targets: [TreemapBox]
    var compact: Bool

    static let empty = UsageMapScene(split: TreemapLayout.Split(local: .zero, cloud: .zero, channel: .zero, ratio: 1, stacked: false, size: .zero),
                                     local: [], cloud: [], badges: [:], links: [:], targets: [], compact: false)

    var size: CGSize { split.size }

    func box(_ id: String) -> TreemapBox? { local.first { $0.id == id } ?? cloud.first { $0.id == id } }
}

nonisolated enum UsageMapSceneBuilder {
    /// What a scene is made from. `revision` changes whenever the data, the selection or the zoom do; the scene cache is
    /// keyed by it, the two focused ids and the width.
    struct Input: Sendable {
        var local: StorageUsageNode
        var cloud: StorageUsageNode
        /// The bytes the local side stands for: the volume at the overview, the focused folder when zoomed.
        var localTotal: Int64
        var cloudTotal: Int64
        var width: CGFloat
        /// A cloud box id → where its item came from.
        var origins: [String: String]
        var revision: Int
        /// The root box's name.
        var machineName: String
    }

    struct Key: Hashable, Sendable {
        var local: String
        var cloud: String
        var width: CGFloat
        var revision: Int
    }

    static func key(_ i: Input) -> Key { Key(local: i.local.id, cloud: i.cloud.id, width: i.width.rounded(), revision: i.revision) }

    static func build(_ input: Input) -> UsageMapScene {
        guard input.width > 0 else { return .empty }
        let (mapSize, compact) = TreemapLayout.mapSize(width: input.width)
        let metrics: TreemapLayout.Metrics = compact ? .compact : .standard
        let split = TreemapLayout.splitScale(localTotal: input.localTotal, cloudTotal: input.cloudTotal, size: mapSize, stacked: compact)
        let local = titled(TreemapLayout.layout(input.local, in: split.local, metrics: metrics, side: .local, stripBelow: compact), input)
        let cloud = input.cloudTotal > 0 && !split.cloud.isEmpty
            ? titled(TreemapLayout.layout(input.cloud, in: split.cloud, metrics: metrics, side: .cloud), input) : []
        var badges: [String: Int] = [:], links: [String: String] = [:]
        // Deepest first, so the first box holding an origin is the one drawn innermost.
        let anchors = local.filter { $0.node.path != nil }.sorted { $0.depth > $1.depth }
        for c in cloud {
            guard let origin = input.origins[c.id], let a = anchors.first(where: { TreemapLayout.contains($0.node.path!, origin) }) else { continue }
            links[c.id] = a.id
            badges[a.id, default: 0] += 1
        }
        // Items whose cloud box is too small to draw still count on their local box.
        for (cid, origin) in input.origins where !cloud.contains(where: { $0.id == cid }) {
            if let a = anchors.first(where: { TreemapLayout.contains($0.node.path!, origin) }) { badges[a.id, default: 0] += 1 }
        }
        let least = Theme.UsageMap.hitTarget
        let targets = (local + cloud)
            .filter { $0.rect.width >= least && $0.rect.height >= least }
            .sorted { $0.rect.width * $0.rect.height > $1.rect.width * $1.rect.height }
            .prefix(Theme.UsageMap.hitTargetCap)
            .sorted { ($0.depth, $0.side == .cloud ? 1 : 0) < ($1.depth, $1.side == .cloud ? 1 : 0) }
        return UsageMapScene(split: split, local: local, cloud: cloud, badges: badges, links: links, targets: Array(targets), compact: compact)
    }

    private static func titled(_ boxes: [TreemapBox], _ input: Input) -> [TreemapBox] {
        var byID: [String: StorageUsageNode] = [:]
        for b in boxes { byID[b.id] = b.node }
        let now = Date.now
        return boxes.map { b in
            var b = b
            b.title = UsageMapText.fullName(b.node, parent: b.parentID.flatMap { byID[$0] }, machine: input.machineName)
            b.sizeText = UsageMapText.size(b.node.bytes)
            b.detail = detail(b, base: b.side == .local ? input.local.bytes : input.cloud.bytes, now: now)
            return b
        }
    }

    static func detail(_ box: TreemapBox, base: Int64, now: Date) -> String {
        let n = box.node
        var parts = [box.title, box.sizeText]
        if base > 0 { parts.append(UsageMapText.percent(Double(n.bytes) / Double(base))) }
        if let c = UsageMapText.changed(n.usedAt, now: now) { parts.append(c) }
        if box.side == .local, n.path != nil, !n.canTrash, n.cacheID == nil { parts.append(UsageMapText.refusal(n.refusal)) }
        if n.cacheID != nil, n.cacheInUse == true { parts.append(String(localized: "In use, can't be cleaned")) }
        if n.partial { parts.append(String(localized: "Still measuring")) }
        return parts.joined(separator: " · ")
    }

    // MARK: The cloud side's tree

    static let cloudRootID = "cloud:root"
    static let incomingKey = "incoming"

    /// The cloud side as a tree the same layout draws: each provider's items (grouped by `group` when they have one,
    /// under one box per provider when there are several), what did not fit in the listing as one box, and the dashed
    /// twins of selected local items on their way (`incoming`).
    static func cloudTree(_ inventories: [(provider: OffloadProvider, inventory: OffloadInventory)], incoming: [StorageUsageNode],
                          otherName: String) -> (root: StorageUsageNode, origins: [String: String], items: [String: (OffloadProvider, OffloadItem)]) {
        var origins: [String: String] = [:]
        var items: [String: (OffloadProvider, OffloadItem)] = [:]
        func node(_ p: OffloadProvider, _ it: OffloadItem) -> StorageUsageNode {
            let id = "cloud:\(p.id):\(it.id)"
            if let o = it.originPath { origins[id] = o }
            items[id] = (p, it)
            return StorageUsageNode(id: id, name: it.name, display: it.originPath, kind: it.kind == .file ? .file : .dir,
                                    category: category(it.kind), bytes: it.bytes, usedAt: it.usedAt)
        }
        func provider(_ p: OffloadProvider, _ inv: OffloadInventory) -> [StorageUsageNode] {
            var loose: [StorageUsageNode] = [], groups: [String: [StorageUsageNode]] = [:], order: [String] = []
            for it in inv.items where it.bytes > 0 {
                let n = node(p, it)
                if let g = it.group, !g.isEmpty {
                    if groups[g] == nil { order.append(g) }
                    groups[g, default: []].append(n)
                } else {
                    loose.append(n)
                }
            }
            var out = loose
            for g in order {
                let kids = groups[g]!.sorted { $0.bytes > $1.bytes }
                out.append(StorageUsageNode(id: "cloud:\(p.id):group:\(g)", name: g, kind: .group, category: kids.first?.category ?? .other,
                                            bytes: kids.reduce(0) { $0 + $1.bytes }, hasChildren: true, children: kids))
            }
            if let more = inv.more, more.bytes > 0 {
                out.append(StorageUsageNode(id: "cloud:\(p.id):more", name: otherName, kind: .other, key: "other", category: .other,
                                            bytes: more.bytes, items: more.count))
            }
            return out.sorted { $0.bytes > $1.bytes }
        }
        var kids: [StorageUsageNode]
        if inventories.count == 1, let only = inventories.first {
            kids = provider(only.provider, only.inventory)
        } else {
            kids = inventories.map { p, inv in
                let k = provider(p, inv)
                return StorageUsageNode(id: "cloud:\(p.id)", name: p.name, kind: .group, category: .other,
                                        bytes: k.reduce(0) { $0 + $1.bytes }, hasChildren: !k.isEmpty, children: k)
            }.filter { $0.bytes > 0 }
        }
        kids += incoming.map {
            StorageUsageNode(id: "incoming:\($0.id)", name: $0.name, path: nil, display: $0.display, kind: $0.kind, key: incomingKey,
                             category: $0.category, bytes: $0.bytes, usedAt: $0.usedAt)
        }
        let root = StorageUsageNode(id: cloudRootID, name: "", kind: .root, category: .other, bytes: kids.reduce(0) { $0 + $1.bytes },
                                    hasChildren: !kids.isEmpty, children: kids)
        return (root, origins, items)
    }

    static func category(_ k: OffloadItemKind) -> StorageUsageCategory {
        switch k {
        case .app, .bundle: .apps
        case .appData: .appdata
        case .file, .dir: .personal
        case .session, .other: .other
        }
    }
}
