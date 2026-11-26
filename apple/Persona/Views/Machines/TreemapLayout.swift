import CoreGraphics
import Foundation
import PersonaWire

// The disk usage map's arithmetic (#14576, docs/server/machine-usage-map.md "The map"), apart from any view so it runs off
// the main actor and is tested on its own (TreemapLayoutTests; docs/apple/performance.md P3, P4, P8): the squarified
// treemap (Bruls, Huizing, van Wijk) ported from the sibling product's dashboard, its nesting thresholds and free
// strip, the shared scale of the local and the cloud side, the Last Used steps, the reclaim suggestions, the free-up
// goal's picks and the "what if" numbers.

/// One drawn box of the map.
nonisolated struct TreemapBox: Identifiable, Hashable, Sendable {
    enum Side: Hashable, Sendable { case local, cloud }

    var id: String
    /// The node without its children (the scene keeps only what it draws).
    var node: StorageUsageNode
    var parentID: String?
    var rect: CGRect
    /// 1 for the focused box's children, deeper inside.
    var depth: Int
    /// Draws its children inside, under a title strip.
    var framed: Bool
    /// A leaf with room for a larger name and its size under it.
    var big: Bool
    /// Opening it shows more (children here, or a folder the node can measure).
    var zoomable: Bool
    var side: Side
    /// What the box says, worked out with the scene (P4): its name and its size.
    var title = ""
    var sizeText = ""
    /// The detail line: name, size, share of the focused box, last changed, why an action is not offered.
    var detail = ""
}

nonisolated enum TreemapLayout {
    /// The layout's numbers, from `Theme.UsageMap` (tests pass their own).
    struct Metrics: Hashable, Sendable {
        var titleStrip = Theme.UsageMap.titleStrip
        var inset = Theme.UsageMap.inset
        var smallest = Theme.UsageMap.smallest
        var nestWidth = Theme.UsageMap.nestWidth
        var nestWidthDeep = Theme.UsageMap.nestWidthDeep
        var nestHeight = Theme.UsageMap.nestHeight
        var nestHeightDeep = Theme.UsageMap.nestHeightDeep
        var bigLeafWidth = Theme.UsageMap.bigLeafWidth
        var bigLeafHeight = Theme.UsageMap.bigLeafHeight
        var freeStripLeast = Theme.UsageMap.freeStripLeast
        var maxDepth = Theme.UsageMap.depth

        static let standard = Metrics()
        static let compact: Metrics = {
            var m = Metrics()
            m.maxDepth = Theme.UsageMap.compactDepth
            return m
        }()
    }

    // MARK: Squarify

    /// Lays `sizes` into `rect` so each area is its share of the total and the boxes stay near square. The answer is in
    /// the input's order; a size of 0 or less gets nil.
    static func squarify(_ sizes: [Double], in rect: CGRect) -> [CGRect?] {
        var out = [CGRect?](repeating: nil, count: sizes.count)
        let order = sizes.indices.filter { sizes[$0] > 0 }.sorted { sizes[$0] > sizes[$1] || (sizes[$0] == sizes[$1] && $0 < $1) }
        let total = order.reduce(0.0) { $0 + sizes[$1] }
        guard total > 0, rect.width > 0, rect.height > 0 else { return out }
        let scale = Double(rect.width * rect.height) / total
        var x = Double(rect.minX), y = Double(rect.minY), w = Double(rect.width), h = Double(rect.height)

        func worst(_ row: [Int], _ side: Double) -> Double {
            var sum = 0.0, most = 0.0, least = Double.infinity
            for i in row {
                let a = sizes[i] * scale
                sum += a
                most = max(most, a)
                least = min(least, a)
            }
            guard sum > 0, side > 0 else { return .infinity }
            return max(side * side * most / (sum * sum), sum * sum / (side * side * least))
        }

        func place(_ row: [Int]) {
            let sum = row.reduce(0.0) { $0 + sizes[$1] * scale }
            if w >= h {
                // A column on the left, as wide as the row's share.
                let cw = h > 0 ? sum / h : 0
                var cy = y
                for i in row {
                    let rh = cw > 0 ? sizes[i] * scale / cw : 0
                    out[i] = CGRect(x: x, y: cy, width: cw, height: rh)
                    cy += rh
                }
                x += cw
                w -= cw
            } else {
                let rh = w > 0 ? sum / w : 0
                var cx = x
                for i in row {
                    let cw = rh > 0 ? sizes[i] * scale / rh : 0
                    out[i] = CGRect(x: cx, y: y, width: cw, height: rh)
                    cx += cw
                }
                y += rh
                h -= rh
            }
        }

        var row: [Int] = []
        for i in order {
            let side = min(w, h)
            if !row.isEmpty && worst(row + [i], side) > worst(row, side) {
                place(row)
                row = []
            }
            row.append(i)
        }
        if !row.isEmpty { place(row) }
        return out
    }

    /// Edges on whole points, so neighbours share one line.
    static func snap(_ r: CGRect) -> CGRect {
        let x = r.minX.rounded(), y = r.minY.rounded()
        return CGRect(x: x, y: y, width: r.maxX.rounded() - x, height: r.maxY.rounded() - y)
    }

    // MARK: Nested layout

    /// The focused node as a nested treemap filling `rect`: its children are the top boxes, each drawing its own inside
    /// while it has room, down to `metrics.maxDepth` levels. Free space takes a strip along the trailing edge (the
    /// bottom when `stripBelow`), so the used boxes stay together.
    static func layout(_ focus: StorageUsageNode, in rect: CGRect, metrics: Metrics = .standard, side: TreemapBox.Side = .local,
                       stripBelow: Bool = false) -> [TreemapBox] {
        var out: [TreemapBox] = []
        func box(_ node: StorageUsageNode, parent: String?, _ raw: CGRect, depth: Int) {
            let r = snap(raw)
            guard r.width >= metrics.smallest, r.height >= metrics.smallest else { return }
            let kids = (node.children ?? []).filter { $0.bytes > 0 }
            let deep = depth >= 2
            let nested = !kids.isEmpty && depth < metrics.maxDepth
                && r.width >= (deep ? metrics.nestWidthDeep : metrics.nestWidth)
                && r.height >= metrics.titleStrip + (deep ? metrics.nestHeightDeep : metrics.nestHeight)
            let big = !nested && r.width >= metrics.bigLeafWidth && r.height >= metrics.bigLeafHeight
            var bare = node
            bare.children = nil
            out.append(TreemapBox(id: node.id, node: bare, parentID: parent, rect: r, depth: depth, framed: nested, big: big,
                                  zoomable: node.zoomable, side: side))
            guard nested else { return }
            let inner = CGRect(x: r.minX + metrics.inset, y: r.minY + metrics.titleStrip,
                               width: r.width - 2 * metrics.inset, height: r.height - metrics.titleStrip - metrics.inset)
            for (kid, cell) in zip(kids, squarify(kids.map { Double($0.bytes) }, in: inner)) {
                if let cell { box(kid, parent: node.id, cell, depth: depth + 1) }
            }
        }
        let kids = (focus.children ?? []).filter { $0.bytes > 0 }
        guard !kids.isEmpty else {
            box(focus, parent: nil, rect, depth: metrics.maxDepth)
            return out
        }
        for (kid, cell) in zip(kids, top(kids, in: rect, metrics: metrics, stripBelow: stripBelow)) {
            if let cell { box(kid, parent: focus.id, cell, depth: 1) }
        }
        return out
    }

    /// The top level's cells, the free box as a strip when it is a sensible one.
    static func top(_ kids: [StorageUsageNode], in rect: CGRect, metrics: Metrics = .standard, stripBelow: Bool = false) -> [CGRect?] {
        let sizes = kids.map { Double($0.bytes) }
        let total = sizes.reduce(0, +)
        guard let fi = kids.firstIndex(where: { $0.kind == .free && $0.bytes > 0 }), total > 0 else { return squarify(sizes, in: rect) }
        let share = sizes[fi] / total
        let strip = ((stripBelow ? rect.height : rect.width) * share).rounded()
        guard strip >= metrics.freeStripLeast, share <= 0.9 else { return squarify(sizes, in: rect) }
        var rest = sizes
        rest[fi] = 0
        if stripBelow {
            var cells = squarify(rest, in: CGRect(x: rect.minX, y: rect.minY, width: rect.width, height: rect.height - strip))
            cells[fi] = CGRect(x: rect.minX, y: rect.maxY - strip, width: rect.width, height: strip)
            return cells
        }
        var cells = squarify(rest, in: CGRect(x: rect.minX, y: rect.minY, width: rect.width - strip, height: rect.height))
        cells[fi] = CGRect(x: rect.maxX - strip, y: rect.minY, width: strip, height: rect.height)
        return cells
    }

    // MARK: Shared scale

    /// Where the two sides go so a byte takes the same area on both.
    struct Split: Hashable, Sendable {
        var local: CGRect
        var cloud: CGRect
        var channel: CGRect
        /// 1 at true scale; N when the cloud side is drawn N times smaller (its "1 : N" chip).
        var ratio: Double
        var stacked: Bool
        /// The whole map.
        var size: CGSize

        var compressed: Bool { ratio > 1.0001 }
    }

    /// Wide: the local side on the left at the map's full height, the cloud side on the right with its share of
    /// `localTotal + cloudTotal` of the width, never narrower than `minCloud` (it gets shorter instead, so its area
    /// stays true) and never wider than `maxShare` (it is compressed, `ratio` > 1). Stacked: the cloud side under the
    /// local one at the full width, with the same rules on its height. `size` is the local map's size when stacked
    /// (the cloud side adds to it), the whole map's otherwise.
    static func splitScale(localTotal: Int64, cloudTotal: Int64, size: CGSize, stacked: Bool,
                           channel: CGFloat = Theme.UsageMap.channel, minCloud: CGFloat = Theme.UsageMap.cloudLeast,
                           maxShare: CGFloat = Theme.UsageMap.cloudMostShare) -> Split {
        let local = Double(max(localTotal, 1)), cloud = Double(max(cloudTotal, 0))
        if stacked {
            let w = size.width, lh = size.height
            let localRect = CGRect(x: 0, y: 0, width: w, height: lh)
            let area = cloud * Double(w * lh) / local
            let cap = lh * maxShare / (1 - maxShare)
            var ch: CGFloat, cw = w, ratio = 1.0
            if cloud <= 0 {
                ch = minCloud
            } else {
                ch = CGFloat(area) / w
                if ch > cap {
                    ratio = Double(ch / cap)
                    ch = cap
                } else if ch < minCloud {
                    ch = minCloud
                    cw = min(w, CGFloat(area) / minCloud)
                }
            }
            let ch0 = ch.rounded(), cw0 = cw.rounded()
            return Split(local: localRect, cloud: CGRect(x: 0, y: lh + channel, width: cw0, height: ch0),
                         channel: CGRect(x: 0, y: lh, width: w, height: channel), ratio: ratio, stacked: true,
                         size: CGSize(width: w, height: lh + channel + ch0))
        }
        let h = size.height, room = max(0, size.width - channel)
        var cw: CGFloat, ch = h, ratio = 1.0
        if cloud <= 0 {
            cw = min(minCloud, room / 2)
        } else {
            cw = min(max(room * CGFloat(cloud / (local + cloud)), minCloud), room * maxShare)
            let scale = Double((room - cw) * h) / local
            ch = CGFloat(cloud * scale) / cw
            if ch > h {
                ratio = Double(ch / h)
                ch = h
            }
        }
        cw = cw.rounded()
        let lw = room - cw
        return Split(local: CGRect(x: 0, y: 0, width: lw, height: h), cloud: CGRect(x: lw + channel, y: 0, width: cw, height: ch.rounded()),
                     channel: CGRect(x: lw, y: 0, width: channel, height: h), ratio: ratio, stacked: false, size: size)
    }

    /// The local map's height for a width, and whether it stacks.
    static func mapSize(width: CGFloat) -> (size: CGSize, compact: Bool) {
        let compact = width < Theme.UsageMap.compactWidth
        let h = compact
            ? min(Theme.UsageMap.compactHeightMost, max(Theme.UsageMap.compactHeightLeast, width * Theme.UsageMap.compactHeightRatio))
            : min(Theme.UsageMap.heightMost, max(Theme.UsageMap.heightLeast, width * Theme.UsageMap.heightRatio))
        return (CGSize(width: width.rounded(.down), height: h.rounded()), compact)
    }

    // MARK: Last Used

    static func ageStep(usedAt: Int64?, now: Date) -> Theme.UsageMap.Age? {
        guard let usedAt, usedAt > 0 else { return nil }
        let age = now.timeIntervalSince1970 - TimeInterval(usedAt)
        let b = Theme.UsageMap.Age.bounds
        if age < b[0] { return .recent }
        if age < b[1] { return .months }
        if age < b[2] { return .halfYear }
        return .year
    }

    // MARK: Suggestions and the goal

    struct Candidate: Identifiable, Hashable, Sendable {
        enum Action: Hashable, Sendable { case clean, trash, offload }

        var node: StorageUsageNode
        var action: Action
        /// The ids from the tree's root down to its parent, to zoom to it.
        var trail: [String]

        var id: String { node.id }
    }

    /// The age past which a big item counts as cold.
    static let coldAge: TimeInterval = 180 * 86_400
    /// Smaller items are not suggested.
    static let suggestLeast: Int64 = 256 * 1_048_576

    /// What could go: an unused catalog cache (Clean), a cache folder the rules allow (Trash), and what a provider takes
    /// (Offload), never inside one already found. `coldOnly` keeps offloads to items untouched for `coldAge` (the
    /// suggestions); the goal takes any a provider allows. Personal folders are never picked for the trash: what goes
    /// there is the user's own choice.
    static func candidates(in root: StorageUsageNode, offloadable: (StorageUsageNode) -> Bool, now: Date, coldOnly: Bool,
                           least: Int64 = 0) -> [Candidate] {
        var out: [Candidate] = []
        func cold(_ n: StorageUsageNode) -> Bool {
            guard let u = n.usedAt, u > 0 else { return false }
            return now.timeIntervalSince1970 - TimeInterval(u) >= coldAge
        }
        func walk(_ n: StorageUsageNode, trail: [String]) {
            let real = n.kind == .dir || n.kind == .file || n.kind == .app
            if real, n.bytes >= least {
                var bare = n
                bare.children = nil
                if n.cacheID != nil, n.cacheInUse != true {
                    out.append(Candidate(node: bare, action: .clean, trail: trail)); return
                }
                if n.category == .cache, n.canTrash {
                    out.append(Candidate(node: bare, action: .trash, trail: trail)); return
                }
                if offloadable(n), !coldOnly || cold(n) {
                    out.append(Candidate(node: bare, action: .offload, trail: trail)); return
                }
            }
            for k in n.children ?? [] { walk(k, trail: trail + [n.id]) }
        }
        for k in root.children ?? [] { walk(k, trail: [root.id]) }
        return out
    }

    /// Up to `limit` suggestions, largest first.
    static func suggestReclaim(in root: StorageUsageNode, offloadable: (StorageUsageNode) -> Bool, now: Date, limit: Int = 5) -> [Candidate] {
        Array(candidates(in: root, offloadable: offloadable, now: now, coldOnly: true, least: suggestLeast)
            .sorted { $0.node.bytes > $1.node.bytes }.prefix(limit))
    }

    /// What the free-up goal selects: unused caches first (largest first), then by size × age, until `goal` is met.
    static func pickForGoal(_ candidates: [Candidate], goal: Int64, now: Date) -> [Candidate] {
        guard goal > 0 else { return [] }
        func score(_ c: Candidate) -> Double {
            let days = c.node.usedAt.map { max(1, (now.timeIntervalSince1970 - TimeInterval($0)) / 86_400) } ?? 1
            return Double(c.node.bytes) * days
        }
        let cleans = candidates.filter { $0.action == .clean }.sorted { $0.node.bytes > $1.node.bytes }
        let rest = candidates.filter { $0.action != .clean }.sorted { score($0) > score($1) }
        var out: [Candidate] = [], sum: Int64 = 0
        for c in cleans + rest where sum < goal {
            out.append(c)
            sum += c.node.bytes
        }
        return out
    }

    /// The tray's "Free after" line.
    struct WhatIf: Hashable, Sendable {
        var freeBefore: Int64
        var freeAfter: Int64
        var gained: Int64
        /// Used share of the volume, 0…1.
        var usedBefore: Double
        var usedAfter: Double
    }

    static func whatIf(volume: StorageUsageVolume, selected: Int64) -> WhatIf {
        let total = Double(max(volume.totalBytes, 1))
        let gained = max(0, min(selected, volume.usedBytes))
        return WhatIf(freeBefore: volume.freeBytes, freeAfter: volume.freeBytes + gained, gained: gained,
                      usedBefore: Double(volume.usedBytes) / total, usedAfter: Double(volume.usedBytes - gained) / total)
    }

    /// The bytes of items some of which may hold others (a folder and a file in it): each byte once.
    static func distinctBytes(_ items: [(path: String?, bytes: Int64)]) -> Int64 {
        let paths = items.compactMap(\.path)
        return items.reduce(0) { sum, it in
            guard let p = it.path else { return sum + it.bytes }
            return paths.contains { $0 != p && contains($0, p) } ? sum : sum + it.bytes
        }
    }

    /// Whether the folder `outer` holds `inner` (or is it).
    static func contains(_ outer: String, _ inner: String) -> Bool {
        inner == outer || inner.hasPrefix(outer.hasSuffix("/") ? outer : outer + "/")
    }
}
