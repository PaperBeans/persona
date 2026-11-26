import CoreGraphics
import Foundation
import PersonaWire
import Testing
@testable import Persona

/// The usage map's arithmetic (#14576, docs/server/machine-usage-map.md "The map"): squarify keeps areas proportional and
/// nothing overlaps, nesting follows the thresholds, the free strip, the shared scale and its clamps, the suggestions,
/// the goal picker and the "what if" numbers.
struct TreemapLayoutTests {
    static func node(_ id: String, _ bytes: Int64, kind: StorageUsageKind = .dir, category: StorageUsageCategory = .personal,
                     usedAt: Int64? = nil, canTrash: Bool = true, canOffload: Bool = true, cacheID: String? = nil,
                     children: [StorageUsageNode]? = nil) -> StorageUsageNode {
        StorageUsageNode(id: "/Users/me/\(id)", name: id, path: "/Users/me/\(id)", kind: kind, category: category, bytes: bytes,
                         usedAt: usedAt, hasChildren: !(children ?? []).isEmpty, children: children, canTrash: canTrash,
                         canOffload: canOffload, cacheID: cacheID, cacheInUse: cacheID == nil ? nil : false)
    }

    static func root(_ kids: [StorageUsageNode]) -> StorageUsageNode {
        StorageUsageNode(id: "group:root", name: "", kind: .root, category: .other, bytes: kids.reduce(0) { $0 + $1.bytes },
                         hasChildren: true, children: kids)
    }

    @Test func squarifyAreasAreProportionalAndDoNotOverlap() {
        let sizes: [Double] = [500, 250, 120, 80, 30, 15, 5, 0]
        let rect = CGRect(x: 0, y: 0, width: 640, height: 360)
        let cells = TreemapLayout.squarify(sizes, in: rect)
        #expect(cells.count == sizes.count && cells.last! == nil) // a size of 0 gets no box
        let total = sizes.reduce(0, +)
        for (s, c) in zip(sizes, cells) where s > 0 {
            let c = try! #require(c)
            #expect(abs(Double(c.width * c.height) / Double(rect.width * rect.height) - s / total) < 0.0001)
            #expect(rect.insetBy(dx: -0.001, dy: -0.001).contains(c))
        }
        let drawn = cells.compactMap { $0 }
        for i in drawn.indices {
            for j in drawn.indices where j > i {
                let o = drawn[i].intersection(drawn[j])
                #expect(o.isNull || o.width * o.height < 0.001, "cells \(i) and \(j) overlap")
            }
        }
    }

    @Test func squarifyIsStableForEqualSizes() {
        let a = TreemapLayout.squarify([10, 10, 10], in: CGRect(x: 0, y: 0, width: 300, height: 100))
        #expect(a == TreemapLayout.squarify([10, 10, 10], in: CGRect(x: 0, y: 0, width: 300, height: 100)))
        #expect(a[0]!.minX < a[1]!.minX)
    }

    @Test func snappedEdgesAreWholePoints() {
        let r = TreemapLayout.snap(CGRect(x: 0.4, y: 10.6, width: 33.3, height: 7.7))
        #expect(r == CGRect(x: 0, y: 11, width: 34, height: 7))
    }

    /// A box draws its children inside only with room: 72 × (22 + 28) at the first level, 96 × (22 + 50) deeper, and never
    /// below the depth limit (2 on a narrow map).
    @Test func nestingFollowsTheThresholds() {
        let grand = (0..<3).map { Self.node("a/b/\($0)", 10) }
        let child = Self.node("a/b", 30, children: grand)
        let big = Self.node("a", 100, children: [child, Self.node("a/c", 70)])
        let boxes = TreemapLayout.layout(Self.root([big]), in: CGRect(x: 0, y: 0, width: 800, height: 500))
        #expect(boxes.first { $0.id == big.id }?.framed == true)
        #expect(boxes.contains { $0.id == grand[0].id && $0.depth == 3 })
        let compact = TreemapLayout.layout(Self.root([big]), in: CGRect(x: 0, y: 0, width: 800, height: 500), metrics: .compact)
        #expect(!compact.contains { $0.depth == 3 })
        #expect(compact.first { $0.id == child.id }?.framed == false) // depth 2 of 2: a leaf
        let small = TreemapLayout.layout(Self.root([big]), in: CGRect(x: 0, y: 0, width: 70, height: 49))
        #expect(small.first { $0.id == big.id }?.framed == false && small.count == 1)
        // Children sit inside their frame, under the title strip.
        let frame = boxes.first { $0.id == big.id }!.rect
        for b in boxes where b.parentID == big.id {
            #expect(b.rect.minY >= frame.minY + Theme.UsageMap.titleStrip && frame.contains(b.rect))
        }
    }

    @Test func freeSpaceTakesAStripAlongTheEdge() {
        let free = StorageUsageNode(id: "group:free", name: "", kind: .free, key: "free", category: .free, bytes: 25)
        let kids = [Self.node("a", 50), Self.node("b", 25), free]
        let rect = CGRect(x: 0, y: 0, width: 400, height: 200)
        let wide = TreemapLayout.top(kids, in: rect)
        #expect(wide[2] == CGRect(x: 300, y: 0, width: 100, height: 200))
        let below = TreemapLayout.top(kids, in: rect, stripBelow: true)
        #expect(below[2] == CGRect(x: 0, y: 150, width: 400, height: 50))
        // A thin strip is an ordinary box.
        let tiny = TreemapLayout.top([Self.node("a", 1000), StorageUsageNode(id: "group:free", name: "", kind: .free, category: .free, bytes: 1)], in: rect)
        #expect(tiny[1]!.width < 28 || tiny[1]!.height < 28)
    }

    /// One scale: a byte is the same area on both sides; a small cloud side keeps its least width and gets shorter, a
    /// large one stops at 45 % and says how much smaller it is drawn.
    @Test func splitScaleSharesOneScaleAndClamps() {
        let size = CGSize(width: 1000, height: 400)
        let s = TreemapLayout.splitScale(localTotal: 300, cloudTotal: 100, size: size, stacked: false, channel: 28, minCloud: 120, maxShare: 0.45)
        let localScale = Double(s.local.width * s.local.height) / 300
        let cloudScale = Double(s.cloud.width * s.cloud.height) / 100
        #expect(abs(localScale - cloudScale) / localScale < 0.01 && !s.compressed)
        #expect(s.local.maxX + 28 == s.cloud.minX)

        let small = TreemapLayout.splitScale(localTotal: 1000, cloudTotal: 10, size: size, stacked: false, channel: 28, minCloud: 120, maxShare: 0.45)
        #expect(small.cloud.width == 120 && small.cloud.height < 400 && !small.compressed)
        let ls = Double(small.local.width * small.local.height) / 1000, cs = Double(small.cloud.width * small.cloud.height) / 10
        #expect(abs(ls - cs) / ls < 0.05)

        let huge = TreemapLayout.splitScale(localTotal: 100, cloudTotal: 900, size: size, stacked: false, channel: 28, minCloud: 120, maxShare: 0.45)
        #expect(abs(huge.cloud.width - (1000 - 28) * 0.45) <= 1 && huge.compressed && huge.ratio > 1)

        let none = TreemapLayout.splitScale(localTotal: 100, cloudTotal: 0, size: size, stacked: false, channel: 28, minCloud: 120, maxShare: 0.45)
        #expect(none.cloud.width == 120 && none.ratio == 1)

        let stacked = TreemapLayout.splitScale(localTotal: 300, cloudTotal: 100, size: CGSize(width: 390, height: 400), stacked: true,
                                               channel: 28, minCloud: 120, maxShare: 0.45)
        #expect(stacked.cloud.minY == 428 && stacked.size.height == 428 + stacked.cloud.height)
        let a = Double(stacked.local.width * stacked.local.height) / 300, b = Double(stacked.cloud.width * stacked.cloud.height) / 100
        #expect(abs(a - b) / a < 0.02)
    }

    @Test func ageStepsFromRecentToOverAYear() {
        let now = Date(timeIntervalSince1970: 2_000_000_000)
        func at(days: Double) -> Int64 { Int64(now.timeIntervalSince1970 - days * 86_400) }
        #expect(TreemapLayout.ageStep(usedAt: at(days: 3), now: now) == .recent)
        #expect(TreemapLayout.ageStep(usedAt: at(days: 90), now: now) == .months)
        #expect(TreemapLayout.ageStep(usedAt: at(days: 200), now: now) == .halfYear)
        #expect(TreemapLayout.ageStep(usedAt: at(days: 400), now: now) == .year)
        #expect(TreemapLayout.ageStep(usedAt: nil, now: now) == nil)
    }

    @Test func suggestionsAreCachesTrashableCachesAndColdOffloadsLargestFirst() {
        let now = Date(timeIntervalSince1970: 2_000_000_000)
        let old = Int64(now.timeIntervalSince1970 - 400 * 86_400), fresh = Int64(now.timeIntervalSince1970 - 86_400)
        let gb: Int64 = 1 << 30
        let tree = Self.root([
            Self.node("go-build", 6 * gb, category: .cache, cacheID: "go-build"),
            Self.node("Caches/x", 3 * gb, category: .cache, canOffload: false),
            Self.node("Movies", 40 * gb, usedAt: old, children: [Self.node("Movies/Old", 30 * gb, usedAt: old)]),
            Self.node("Work", 50 * gb, usedAt: fresh),
            Self.node("Tiny", 1024, category: .cache),
        ])
        let s = TreemapLayout.suggestReclaim(in: tree, offloadable: { $0.canOffload }, now: now)
        #expect(s.map(\.node.name) == ["Movies", "go-build", "Caches/x"])
        #expect(s.map(\.action) == [.offload, .clean, .trash])
        #expect(s[0].trail == ["group:root"])
        // Without a provider there is nothing to offload.
        #expect(!TreemapLayout.suggestReclaim(in: tree, offloadable: { _ in false }, now: now).contains { $0.action == .offload })
    }

    @Test func goalPicksUnusedCachesFirstThenSizeTimesAge() {
        let now = Date(timeIntervalSince1970: 2_000_000_000)
        func days(_ d: Double) -> Int64 { Int64(now.timeIntervalSince1970 - d * 86_400) }
        let tree = Self.root([
            Self.node("cache", 5, category: .cache, cacheID: "npm"),
            Self.node("big-new", 100, usedAt: days(1)),
            Self.node("mid-old", 40, usedAt: days(500)),
            Self.node("doc", 30, usedAt: days(10), canOffload: false),
        ])
        let c = TreemapLayout.candidates(in: tree, offloadable: { $0.canOffload }, now: now, coldOnly: false)
        #expect(!c.contains { $0.node.name == "doc" }) // personal files are never picked for the trash
        let picks = TreemapLayout.pickForGoal(c, goal: 30, now: now)
        #expect(picks.map(\.node.name) == ["cache", "mid-old"]) // 40 × 500 days scores above 100 × 1 day
        #expect(TreemapLayout.pickForGoal(c, goal: 0, now: now).isEmpty)
        #expect(TreemapLayout.pickForGoal(c, goal: 10_000, now: now).count == 3)
    }

    @Test func whatIfNumbers() {
        let v = StorageUsageVolume(totalBytes: 1000, usedBytes: 910, freeBytes: 90)
        let w = TreemapLayout.whatIf(volume: v, selected: 150)
        #expect(w.freeAfter == 240 && w.gained == 150)
        #expect(abs(w.usedBefore - 0.91) < 1e-9 && abs(w.usedAfter - 0.76) < 1e-9)
        #expect(TreemapLayout.whatIf(volume: v, selected: 5000).gained == 910) // never more than is used
        #expect(TreemapLayout.distinctBytes([("/a", 10), ("/a/b", 4), ("/c", 1)]) == 11)
        #expect(TreemapLayout.contains("/a", "/a/b") && !TreemapLayout.contains("/a", "/ab"))
    }

    @Test func sceneLinksCloudItemsToTheirLocalFolderAndCapsTargets() {
        let movies = Self.node("Movies", 400)
        let local = Self.root([movies, Self.node("Work", 600)])
        let inv = OffloadInventory(items: [OffloadItem(id: "m1", name: "Old", bytes: 200, originPath: "/Users/me/Movies/Old")])
        let p = OffloadProvider(extension: "example", contribution: "cloud", name: "Example Cloud")
        let tree = UsageMapSceneBuilder.cloudTree([(p, inv)], incoming: [], otherName: "Other")
        let scene = UsageMapSceneBuilder.build(.init(local: local, cloud: tree.root, localTotal: 1000, cloudTotal: tree.root.bytes, width: 900,
                                                     origins: tree.origins, revision: 1, machineName: "Mac"))
        #expect(scene.cloud.count == 1 && scene.links[scene.cloud[0].id] == movies.id && scene.badges[movies.id] == 1)
        #expect(scene.targets.count <= Theme.UsageMap.hitTargetCap && scene.targets.allSatisfy { $0.rect.width >= 24 && $0.rect.height >= 24 })
        #expect(scene.local.allSatisfy { !$0.title.isEmpty && !$0.sizeText.isEmpty })
        #expect(!scene.compact && scene.split.cloud.minX > scene.split.local.maxX)
        let narrow = UsageMapSceneBuilder.build(.init(local: local, cloud: tree.root, localTotal: 1000, cloudTotal: tree.root.bytes, width: 390,
                                                      origins: tree.origins, revision: 1, machineName: "Mac"))
        #expect(narrow.compact && narrow.split.stacked && narrow.split.cloud.minY > narrow.split.local.maxY)
    }
}
