import Foundation
import Testing

@testable import PersonaWire

/// The usage map's wire (docs/server/machine-usage-map.md): the doc's own example decodes, fixed boxes without flags
/// read as refused, and values a newer hub adds do not fail the answer.
@Suite struct MachineUsageMapTests {
    /// The example of "machine.storage.usage" in docs/server/machine-usage-map.md, as written there.
    static let docExample = #"""
    {"machine_id": "mac", "at": 1767000000, "os": "darwin", "path": "", "state": "scanning", "partial": false, "cached": false,
     "scanned_at": 1767000000, "duration_ms": 1480, "home": "/Users/me",
     "volume": {"mount": "/", "total_bytes": 245107195904, "used_bytes": 221000000000, "free_bytes": 24107195904},
     "node": {"id": "group:root", "name": "", "kind": "root", "category": "other", "bytes": 245107195904, "has_children": true,
      "children": [
       {"id": "group:apps", "name": "", "key": "apps", "kind": "group", "category": "apps", "bytes": 41000000000, "has_children": true,
        "children": [{"id": "/Applications/Example.app", "name": "Example", "path": "/Applications/Example.app", "display": "/Applications/Example.app",
          "kind": "app", "category": "apps", "bytes": 9100000000, "used_at": 1741000000, "has_children": true,
          "can_trash": false, "can_offload": true, "refusal": "app_bundle", "app": {"bundle_id": "com.example.app", "version": "3.2"}}]},
       {"id": "/Users/me/Library/Caches/go-build", "name": "go-build", "path": "/Users/me/Library/Caches/go-build", "display": "~/Library/Caches/go-build",
        "kind": "dir", "category": "cache", "bytes": 6200000000, "has_children": true, "can_trash": true, "can_offload": false,
        "cache_id": "go-build", "cache_in_use": false},
       {"id": "group:free", "name": "", "key": "free", "kind": "free", "category": "free", "bytes": 24107195904, "has_children": false}]}}
    """#

    @Test func decodesTheDocExample() throws {
        let r = try JSON.decoder.decode(StorageUsageReport.self, from: Data(Self.docExample.utf8))
        #expect(r.state == .scanning)
        #expect(r.volume?.totalBytes == 245_107_195_904)
        let kids = try #require(r.node.children)
        #expect(kids.count == 3)
        #expect(kids[0].canTrash == false && kids[0].canOffload == false) // a fixed box leaves the flags out
        let app = try #require(kids[0].children?.first)
        #expect(app.kind == .app && app.refusal == "app_bundle" && app.app?.bundleID == "com.example.app")
        #expect(!app.zoomable) // an app's bundle is not walked from the map
        #expect(kids[1].cacheID == "go-build" && kids[1].cacheInUse == false && kids[1].zoomable)
        #expect(kids[2].kind == .free && !kids[2].zoomable)
    }

    @Test func unknownValuesReadAsTheFallback() throws {
        let raw = #"{"id": "x", "kind": "hologram", "category": "fancy", "bytes": -4}"#
        let n = try JSON.decoder.decode(StorageUsageNode.self, from: Data(raw.utf8))
        #expect(n.kind == .other && n.category == .other && n.bytes == 0)
        let job = try JSON.decoder.decode(OffloadJob.self, from: Data(#"{"state": "paused", "phase": "teleport", "done_bytes": 5, "total_bytes": 10}"#.utf8))
        #expect(job.state == .running && job.phase == .other && job.fraction == 0.5)
    }

    @Test func providersAndInventory() throws {
        let p = try JSON.decoder.decode(OffloadProvidersResponse.self, from: Data(#"""
        {"providers": [{"extension": "example", "id": "cloud", "name": "Example Cloud", "kinds": ["file", "dir", "app"], "restore": true}]}
        """#.utf8)).providers
        #expect(p.first?.id == "example/cloud" && p.first?.takes(.app) == true && p.first?.takes(.free) == false)
        let inv = try JSON.decoder.decode(OffloadInventory.self, from: Data(#"""
        {"state": "ready", "at": 1, "total_bytes": 30, "items": [{"id": "a", "name": "Movies", "kind": "dir", "bytes": 30,
         "origin_path": "/Users/me/Movies/Old", "restorable": true}], "more": {"count": 2, "bytes": 3}}
        """#.utf8))
        #expect(inv.items.first?.originPath == "/Users/me/Movies/Old" && inv.more?.count == 2 && inv.totalBytes == 30)
    }

    @Test func runBodyIsSnakeCase() throws {
        let body = OffloadRunRequest(machine: "mac", action: .offload, paths: ["/a"], dryRun: true)
        let text = String(decoding: try JSON.encoder.encode(body), as: UTF8.self)
        #expect(text.contains("\"dry_run\":true") && text.contains("\"action\":\"offload\"") && !text.contains("install"))
    }

    @Test func trashIDsAreTheTrashedOnes() throws {
        let r = try JSON.decoder.decode(StorageTrashResult.self, from: Data(#"""
        {"machine_id": "mac", "dry_run": false, "freed_bytes": 0, "pending_bytes": 9, "available_before": 1, "available_after": 1,
         "items": [{"path": "/a", "display": "~/a", "bytes": 9, "status": "trashed", "trash_id": "t1"},
                   {"path": "/b", "display": "~/b", "bytes": 4, "status": "refused", "refusal": "credentials"}]}
        """#.utf8))
        #expect(r.trashIDs == ["t1"] && r.pendingBytes == 9 && r.items[1].refusal == "credentials")
    }
}
