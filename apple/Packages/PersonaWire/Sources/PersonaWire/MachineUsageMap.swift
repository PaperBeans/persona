import Foundation

// The disk usage map's wire (#14576, docs/server/machine-usage-map.md): the node's usage scan and trash answers, the
// existing cache cleanup's answer, and the `disk_offload` extension point's providers, inventory, runs and jobs. Field
// names are the hub's snake_case. Values a newer hub may add (a kind, a state, a phase) decode as `other` / `unknown`
// instead of failing the whole answer, and flags a node leaves out on a fixed box (`can_trash` on a group) read false.

/// A string enum that reads an unknown value as its fallback case.
public protocol LenientWireEnum: RawRepresentable, Codable, Hashable, Sendable where RawValue == String {
    static var fallback: Self { get }
}

public extension LenientWireEnum {
    init(from decoder: any Decoder) throws {
        let raw = (try? decoder.singleValueContainer().decode(String.self)) ?? ""
        self = Self(rawValue: raw) ?? Self.fallback
    }

    func encode(to encoder: any Encoder) throws {
        var c = encoder.singleValueContainer()
        try c.encode(rawValue)
    }
}

// MARK: - The usage scan

public nonisolated enum StorageUsageKind: String, LenientWireEnum {
    case root, group, dir, file, app, mount, other, free, unmeasured
    public static var fallback: Self { .other }
}

/// What a box is colored by in the Kind tint.
public nonisolated enum StorageUsageCategory: String, LenientWireEnum, CaseIterable {
    case apps, personal, appdata, cache, system, free, other
    public static var fallback: Self { .other }
}

public nonisolated enum StorageUsageState: String, LenientWireEnum {
    case scanning, ready
    public static var fallback: Self { .ready }
}

public nonisolated struct StorageUsageVolume: Codable, Hashable, Sendable {
    public var mount: String
    public var totalBytes: Int64
    public var usedBytes: Int64
    public var freeBytes: Int64

    public enum CodingKeys: String, CodingKey {
        case mount
        case totalBytes = "total_bytes"
        case usedBytes = "used_bytes"
        case freeBytes = "free_bytes"
    }

    public init(mount: String = "/", totalBytes: Int64, usedBytes: Int64, freeBytes: Int64) {
        self.mount = mount
        self.totalBytes = totalBytes
        self.usedBytes = usedBytes
        self.freeBytes = freeBytes
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        mount = try c.decodeIfPresent(String.self, forKey: .mount) ?? ""
        totalBytes = try c.decodeIfPresent(Int64.self, forKey: .totalBytes) ?? 0
        usedBytes = try c.decodeIfPresent(Int64.self, forKey: .usedBytes) ?? 0
        freeBytes = try c.decodeIfPresent(Int64.self, forKey: .freeBytes) ?? 0
    }
}

public nonisolated struct StorageUsageApp: Codable, Hashable, Sendable {
    public var bundleID: String
    public var version: String?

    public enum CodingKeys: String, CodingKey {
        case bundleID = "bundle_id"
        case version
    }

    public init(bundleID: String, version: String? = nil) {
        self.bundleID = bundleID
        self.version = version
    }
}

/// One box of the map: a fixed group, a folder, a file, an app, another filesystem, free space.
public nonisolated struct StorageUsageNode: Codable, Hashable, Sendable, Identifiable {
    /// The absolute path, or `group:<key>` for a fixed box.
    public var id: String
    /// "" on a fixed box: the client names it from `key`.
    public var name: String
    public var path: String?
    /// The path as people read it (`~/…`).
    public var display: String?
    public var kind: StorageUsageKind
    public var key: String?
    public var category: StorageUsageCategory
    /// Allocated bytes: what frees when the item goes.
    public var bytes: Int64
    /// How many entries an `other` box merges.
    public var items: Int?
    /// Unix seconds of the newest modification seen in the subtree.
    public var usedAt: Int64?
    public var hasChildren: Bool
    public var partial: Bool
    public var children: [StorageUsageNode]?
    public var canTrash: Bool
    public var canOffload: Bool
    /// The first protection rule that refuses Move to Trash.
    public var refusal: String?
    /// The folder is one cache of the existing cleanup catalog.
    public var cacheID: String?
    public var cacheInUse: Bool?
    public var app: StorageUsageApp?

    public enum CodingKeys: String, CodingKey {
        case id, name, path, display, kind, key, category, bytes, items, partial, children, refusal, app
        case usedAt = "used_at"
        case hasChildren = "has_children"
        case canTrash = "can_trash"
        case canOffload = "can_offload"
        case cacheID = "cache_id"
        case cacheInUse = "cache_in_use"
    }

    public init(id: String, name: String, path: String? = nil, display: String? = nil, kind: StorageUsageKind,
                key: String? = nil, category: StorageUsageCategory, bytes: Int64, items: Int? = nil, usedAt: Int64? = nil,
                hasChildren: Bool = false, partial: Bool = false, children: [StorageUsageNode]? = nil,
                canTrash: Bool = false, canOffload: Bool = false, refusal: String? = nil,
                cacheID: String? = nil, cacheInUse: Bool? = nil, app: StorageUsageApp? = nil) {
        self.id = id
        self.name = name
        self.path = path
        self.display = display
        self.kind = kind
        self.key = key
        self.category = category
        self.bytes = bytes
        self.items = items
        self.usedAt = usedAt
        self.hasChildren = hasChildren
        self.partial = partial
        self.children = children
        self.canTrash = canTrash
        self.canOffload = canOffload
        self.refusal = refusal
        self.cacheID = cacheID
        self.cacheInUse = cacheInUse
        self.app = app
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? ""
        path = try c.decodeIfPresent(String.self, forKey: .path)
        display = try c.decodeIfPresent(String.self, forKey: .display)
        kind = try c.decodeIfPresent(StorageUsageKind.self, forKey: .kind) ?? .other
        key = try c.decodeIfPresent(String.self, forKey: .key)
        category = try c.decodeIfPresent(StorageUsageCategory.self, forKey: .category) ?? .other
        bytes = max(0, try c.decodeIfPresent(Int64.self, forKey: .bytes) ?? 0)
        items = try c.decodeIfPresent(Int.self, forKey: .items)
        usedAt = try c.decodeIfPresent(Int64.self, forKey: .usedAt)
        children = try c.decodeIfPresent([StorageUsageNode].self, forKey: .children)
        hasChildren = try c.decodeIfPresent(Bool.self, forKey: .hasChildren) ?? !(children ?? []).isEmpty
        partial = try c.decodeIfPresent(Bool.self, forKey: .partial) ?? false
        canTrash = try c.decodeIfPresent(Bool.self, forKey: .canTrash) ?? false
        canOffload = try c.decodeIfPresent(Bool.self, forKey: .canOffload) ?? false
        refusal = try c.decodeIfPresent(String.self, forKey: .refusal)
        cacheID = try c.decodeIfPresent(String.self, forKey: .cacheID)
        cacheInUse = try c.decodeIfPresent(Bool.self, forKey: .cacheInUse)
        app = try c.decodeIfPresent(StorageUsageApp.self, forKey: .app)
    }

    /// Whether opening it can show more: children already here, or a folder the node can measure.
    public var zoomable: Bool { !(children ?? []).isEmpty || (hasChildren && path != nil && kind != .app) }
}

/// `GET /api/v1/machines/{mid}/storage/usage`: the overview (`path` "") or one folder.
public nonisolated struct StorageUsageReport: Codable, Hashable, Sendable {
    public var machineID: String
    public var at: Int64
    public var os: String
    public var path: String
    public var state: StorageUsageState
    public var partial: Bool
    public var cached: Bool
    public var scannedAt: Int64
    public var durationMS: Int64
    public var home: String
    public var volume: StorageUsageVolume?
    public var node: StorageUsageNode

    public enum CodingKeys: String, CodingKey {
        case at, os, path, state, partial, cached, home, volume, node
        case machineID = "machine_id"
        case scannedAt = "scanned_at"
        case durationMS = "duration_ms"
    }

    public init(machineID: String = "", at: Int64 = 0, os: String = "", path: String = "", state: StorageUsageState = .ready,
                partial: Bool = false, cached: Bool = false, scannedAt: Int64 = 0, durationMS: Int64 = 0, home: String = "",
                volume: StorageUsageVolume? = nil, node: StorageUsageNode) {
        self.machineID = machineID
        self.at = at
        self.os = os
        self.path = path
        self.state = state
        self.partial = partial
        self.cached = cached
        self.scannedAt = scannedAt
        self.durationMS = durationMS
        self.home = home
        self.volume = volume
        self.node = node
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        machineID = try c.decodeIfPresent(String.self, forKey: .machineID) ?? ""
        at = try c.decodeIfPresent(Int64.self, forKey: .at) ?? 0
        os = try c.decodeIfPresent(String.self, forKey: .os) ?? ""
        path = try c.decodeIfPresent(String.self, forKey: .path) ?? ""
        state = try c.decodeIfPresent(StorageUsageState.self, forKey: .state) ?? .ready
        partial = try c.decodeIfPresent(Bool.self, forKey: .partial) ?? false
        cached = try c.decodeIfPresent(Bool.self, forKey: .cached) ?? false
        scannedAt = try c.decodeIfPresent(Int64.self, forKey: .scannedAt) ?? 0
        durationMS = try c.decodeIfPresent(Int64.self, forKey: .durationMS) ?? 0
        home = try c.decodeIfPresent(String.self, forKey: .home) ?? ""
        volume = try c.decodeIfPresent(StorageUsageVolume.self, forKey: .volume)
        node = try c.decode(StorageUsageNode.self, forKey: .node)
    }
}

// MARK: - Trash, undo, purge

public nonisolated enum StorageTrashStatus: String, LenientWireEnum {
    case preview, trashed, refused, missing, failed
    public static var fallback: Self { .failed }
}

public nonisolated struct StorageTrashItem: Codable, Hashable, Sendable {
    public var path: String
    public var display: String
    public var bytes: Int64
    public var status: StorageTrashStatus
    public var refusal: String?
    public var error: String?
    public var trashID: String?

    public enum CodingKeys: String, CodingKey {
        case path, display, bytes, status, refusal, error
        case trashID = "trash_id"
    }

    public init(path: String, display: String = "", bytes: Int64 = 0, status: StorageTrashStatus, refusal: String? = nil,
                error: String? = nil, trashID: String? = nil) {
        self.path = path
        self.display = display
        self.bytes = bytes
        self.status = status
        self.refusal = refusal
        self.error = error
        self.trashID = trashID
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        path = try c.decodeIfPresent(String.self, forKey: .path) ?? ""
        display = try c.decodeIfPresent(String.self, forKey: .display) ?? path
        bytes = try c.decodeIfPresent(Int64.self, forKey: .bytes) ?? 0
        status = try c.decodeIfPresent(StorageTrashStatus.self, forKey: .status) ?? .failed
        refusal = try c.decodeIfPresent(String.self, forKey: .refusal)
        error = try c.decodeIfPresent(String.self, forKey: .error)
        trashID = try c.decodeIfPresent(String.self, forKey: .trashID)
    }
}

/// `POST …/storage/trash`. Moving to the trash frees nothing until it is emptied: `pending_bytes` says how much waits.
public nonisolated struct StorageTrashResult: Codable, Hashable, Sendable {
    public var machineID: String
    public var dryRun: Bool
    public var freedBytes: Int64
    public var pendingBytes: Int64
    public var availableBefore: Int64
    public var availableAfter: Int64
    public var items: [StorageTrashItem]

    public enum CodingKeys: String, CodingKey {
        case items
        case machineID = "machine_id"
        case dryRun = "dry_run"
        case freedBytes = "freed_bytes"
        case pendingBytes = "pending_bytes"
        case availableBefore = "available_before"
        case availableAfter = "available_after"
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        machineID = try c.decodeIfPresent(String.self, forKey: .machineID) ?? ""
        dryRun = try c.decodeIfPresent(Bool.self, forKey: .dryRun) ?? false
        freedBytes = try c.decodeIfPresent(Int64.self, forKey: .freedBytes) ?? 0
        pendingBytes = try c.decodeIfPresent(Int64.self, forKey: .pendingBytes) ?? 0
        availableBefore = try c.decodeIfPresent(Int64.self, forKey: .availableBefore) ?? 0
        availableAfter = try c.decodeIfPresent(Int64.self, forKey: .availableAfter) ?? 0
        items = try c.decodeIfPresent([StorageTrashItem].self, forKey: .items) ?? []
    }

    /// The ids of what this call put in the trash: what Undo and Delete Permanently name.
    public var trashIDs: [String] { items.filter { $0.status == .trashed }.compactMap(\.trashID) }
}

/// `POST …/storage/trash/undo`.
public nonisolated struct StorageUntrashResult: Codable, Hashable, Sendable {
    public struct Restored: Codable, Hashable, Sendable {
        public var path: String
    }

    public var restored: [Restored]

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        restored = try c.decodeIfPresent([Restored].self, forKey: .restored) ?? []
    }

    public enum CodingKeys: String, CodingKey { case restored }
}

/// `POST …/storage/trash/purge`: what Delete Permanently returned.
public nonisolated struct StoragePurgeResult: Codable, Hashable, Sendable {
    public struct Item: Codable, Hashable, Sendable {
        public var trashID: String
        public var status: String
        public var error: String?

        public enum CodingKeys: String, CodingKey {
            case status, error
            case trashID = "trash_id"
        }
    }

    public var freedBytes: Int64
    public var availableBefore: Int64
    public var availableAfter: Int64
    public var items: [Item]

    public enum CodingKeys: String, CodingKey {
        case items
        case freedBytes = "freed_bytes"
        case availableBefore = "available_before"
        case availableAfter = "available_after"
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        freedBytes = try c.decodeIfPresent(Int64.self, forKey: .freedBytes) ?? 0
        availableBefore = try c.decodeIfPresent(Int64.self, forKey: .availableBefore) ?? 0
        availableAfter = try c.decodeIfPresent(Int64.self, forKey: .availableAfter) ?? 0
        items = try c.decodeIfPresent([Item].self, forKey: .items) ?? []
    }

    /// Items the node could not delete (Windows answers `unsupported` in version 1).
    public var unsupported: Bool { !items.isEmpty && items.allSatisfy { $0.status == "unsupported" } }
}

/// `POST /api/v1/machines/{mid}/cleanup {ids}`: the existing cache cleanup (#8037), reused as it is.
public nonisolated struct MachineCleanupResult: Codable, Hashable, Sendable {
    public struct Item: Codable, Hashable, Sendable {
        public var id: String
        public var bytes: Int64
        public var status: String
        public var error: String?
    }

    public var freedBytes: Int64
    public var items: [Item]

    public enum CodingKeys: String, CodingKey {
        case items
        case freedBytes = "freed_bytes"
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        freedBytes = try c.decodeIfPresent(Int64.self, forKey: .freedBytes) ?? 0
        items = try c.decodeIfPresent([Item].self, forKey: .items) ?? []
    }
}

// MARK: - The disk_offload extension point

/// One enabled `disk_offload` contribution (`GET /api/v1/extensions/-/offload?machine=`). Core never names the product:
/// `name` is the extension's own.
public nonisolated struct OffloadProvider: Codable, Hashable, Sendable, Identifiable {
    public var `extension`: String
    public var contribution: String
    public var name: String
    /// What it takes: `file`, `dir`, `app` (default file and dir).
    public var kinds: [String]
    /// Items can be brought back.
    public var restore: Bool

    public var id: String { "\(`extension`)/\(contribution)" }

    public enum CodingKeys: String, CodingKey {
        case `extension`, name, kinds, restore
        case contribution = "id"
    }

    public init(extension ext: String, contribution: String, name: String, kinds: [String] = ["file", "dir"], restore: Bool = true) {
        self.extension = ext
        self.contribution = contribution
        self.name = name
        self.kinds = kinds
        self.restore = restore
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        `extension` = try c.decode(String.self, forKey: .extension)
        contribution = try c.decode(String.self, forKey: .contribution)
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? contribution
        kinds = try c.decodeIfPresent([String].self, forKey: .kinds) ?? ["file", "dir"]
        restore = try c.decodeIfPresent(Bool.self, forKey: .restore) ?? false
    }

    /// Whether it takes a box of this kind.
    public func takes(_ kind: StorageUsageKind) -> Bool {
        switch kind {
        case .file: kinds.contains("file")
        case .dir: kinds.contains("dir")
        case .app: kinds.contains("app")
        default: false
        }
    }
}

public nonisolated struct OffloadProvidersResponse: Codable, Hashable, Sendable {
    public var providers: [OffloadProvider]

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        providers = try c.decodeIfPresent([OffloadProvider].self, forKey: .providers) ?? []
    }

    public enum CodingKeys: String, CodingKey { case providers }
}

/// The program a provider runs on the computer, when it needs one: named in the install confirmation.
public nonisolated struct OffloadTool: Codable, Hashable, Sendable {
    public var name: String
    public var title: String?
    public var version: String?
    public var installed: Bool
    /// One sentence in the caller's language: where it comes from and where it is installed.
    public var source: String?
    public var detail: String?

    public init(name: String, title: String? = nil, version: String? = nil, installed: Bool = false, source: String? = nil, detail: String? = nil) {
        self.name = name
        self.title = title
        self.version = version
        self.installed = installed
        self.source = source
        self.detail = detail
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? ""
        title = try c.decodeIfPresent(String.self, forKey: .title)
        version = try c.decodeIfPresent(String.self, forKey: .version)
        installed = try c.decodeIfPresent(Bool.self, forKey: .installed) ?? false
        source = try c.decodeIfPresent(String.self, forKey: .source)
        detail = try c.decodeIfPresent(String.self, forKey: .detail)
    }

    public enum CodingKeys: String, CodingKey { case name, title, version, installed, source, detail }

    /// What the confirmation calls it.
    public var shownName: String { (title?.isEmpty == false ? title : nil) ?? name }
}

public nonisolated enum OffloadInventoryState: String, LenientWireEnum {
    case ready
    case needsInstall = "needs_install"
    case needsSetup = "needs_setup"
    case unavailable
    public static var fallback: Self { .unavailable }
}

public nonisolated enum OffloadItemKind: String, LenientWireEnum {
    case file, dir, app
    case appData = "app_data"
    case bundle, session, other
    public static var fallback: Self { .other }
}

/// One item already offloaded: `bytes` is the size it had on the disk, so both sides of the map share one scale.
public nonisolated struct OffloadItem: Codable, Hashable, Sendable, Identifiable {
    public var id: String
    public var name: String
    public var kind: OffloadItemKind
    public var bytes: Int64
    public var storedBytes: Int64?
    /// Where it was, and where a restore puts it.
    public var originPath: String?
    /// The placeholder left behind, if any.
    public var stubPath: String?
    /// What of it is cached on the computer now.
    public var localBytes: Int64?
    public var offloadedAt: Int64?
    public var usedAt: Int64?
    public var group: String?
    public var restorable: Bool

    public enum CodingKeys: String, CodingKey {
        case id, name, kind, bytes, group, restorable
        case storedBytes = "stored_bytes"
        case originPath = "origin_path"
        case stubPath = "stub_path"
        case localBytes = "local_bytes"
        case offloadedAt = "offloaded_at"
        case usedAt = "used_at"
    }

    public init(id: String, name: String, kind: OffloadItemKind = .dir, bytes: Int64, storedBytes: Int64? = nil, originPath: String? = nil,
                stubPath: String? = nil, localBytes: Int64? = nil, offloadedAt: Int64? = nil, usedAt: Int64? = nil, group: String? = nil,
                restorable: Bool = true) {
        self.id = id
        self.name = name
        self.kind = kind
        self.bytes = bytes
        self.storedBytes = storedBytes
        self.originPath = originPath
        self.stubPath = stubPath
        self.localBytes = localBytes
        self.offloadedAt = offloadedAt
        self.usedAt = usedAt
        self.group = group
        self.restorable = restorable
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? id
        kind = try c.decodeIfPresent(OffloadItemKind.self, forKey: .kind) ?? .other
        bytes = max(0, try c.decodeIfPresent(Int64.self, forKey: .bytes) ?? 0)
        storedBytes = try c.decodeIfPresent(Int64.self, forKey: .storedBytes)
        originPath = try c.decodeIfPresent(String.self, forKey: .originPath)
        stubPath = try c.decodeIfPresent(String.self, forKey: .stubPath)
        localBytes = try c.decodeIfPresent(Int64.self, forKey: .localBytes)
        offloadedAt = try c.decodeIfPresent(Int64.self, forKey: .offloadedAt)
        usedAt = try c.decodeIfPresent(Int64.self, forKey: .usedAt)
        group = try c.decodeIfPresent(String.self, forKey: .group)
        restorable = try c.decodeIfPresent(Bool.self, forKey: .restorable) ?? false
    }
}

/// `GET /api/v1/extensions/{id}/-/offload/{cid}/inventory?machine=`: what is already offloaded from this computer.
public nonisolated struct OffloadInventory: Codable, Hashable, Sendable {
    public struct More: Codable, Hashable, Sendable {
        public var count: Int
        public var bytes: Int64

        public init(count: Int, bytes: Int64) {
            self.count = count
            self.bytes = bytes
        }
    }

    public var state: OffloadInventoryState
    public var detail: String?
    public var tool: OffloadTool?
    public var at: Int64
    public var totalBytes: Int64
    public var storedBytes: Int64?
    public var partial: Bool
    public var items: [OffloadItem]
    /// What did not fit in the 500 items listed.
    public var more: More?

    public enum CodingKeys: String, CodingKey {
        case state, detail, tool, at, partial, items, more
        case totalBytes = "total_bytes"
        case storedBytes = "stored_bytes"
    }

    public init(state: OffloadInventoryState = .ready, detail: String? = nil, tool: OffloadTool? = nil, at: Int64 = 0, totalBytes: Int64? = nil,
                storedBytes: Int64? = nil, partial: Bool = false, items: [OffloadItem], more: More? = nil) {
        self.state = state
        self.detail = detail
        self.tool = tool
        self.at = at
        self.totalBytes = totalBytes ?? items.reduce(0) { $0 + $1.bytes } + (more?.bytes ?? 0)
        self.storedBytes = storedBytes
        self.partial = partial
        self.items = items
        self.more = more
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        state = try c.decodeIfPresent(OffloadInventoryState.self, forKey: .state) ?? .unavailable
        detail = try c.decodeIfPresent(String.self, forKey: .detail)
        tool = try c.decodeIfPresent(OffloadTool.self, forKey: .tool)
        at = try c.decodeIfPresent(Int64.self, forKey: .at) ?? 0
        items = try c.decodeIfPresent([OffloadItem].self, forKey: .items) ?? []
        more = try c.decodeIfPresent(More.self, forKey: .more)
        totalBytes = try c.decodeIfPresent(Int64.self, forKey: .totalBytes) ?? items.reduce(0) { $0 + $1.bytes } + (more?.bytes ?? 0)
        storedBytes = try c.decodeIfPresent(Int64.self, forKey: .storedBytes)
        partial = try c.decodeIfPresent(Bool.self, forKey: .partial) ?? false
    }
}

/// `POST …/offload/{cid}/run`: the body. Without `dry_run` or `confirm` the manager refuses it.
public nonisolated struct OffloadRunRequest: Codable, Hashable, Sendable {
    public enum Action: String, Codable, Sendable { case offload, restore }

    public var machine: String
    public var action: Action
    public var paths: [String]?
    public var items: [String]?
    public var dryRun: Bool?
    public var confirm: Bool?
    public var install: Bool?

    public enum CodingKeys: String, CodingKey {
        case machine, action, paths, items, confirm, install
        case dryRun = "dry_run"
    }

    public init(machine: String, action: Action, paths: [String]? = nil, items: [String]? = nil, dryRun: Bool? = nil,
                confirm: Bool? = nil, install: Bool? = nil) {
        self.machine = machine
        self.action = action
        self.paths = paths
        self.items = items
        self.dryRun = dryRun
        self.confirm = confirm
        self.install = install
    }
}

/// The run's answer: 202 `{job}` when started, 200 `{status: "ok", items}` for a dry run.
public nonisolated struct OffloadRunResult: Codable, Hashable, Sendable {
    public struct Check: Codable, Hashable, Sendable {
        public var path: String?
        public var id: String?
        public var ok: Bool
        public var reason: String?
        public var bytes: Int64?
    }

    public var status: String?
    public var job: String?
    public var items: [Check]
    public var tool: OffloadTool?

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        status = try c.decodeIfPresent(String.self, forKey: .status)
        job = try c.decodeIfPresent(String.self, forKey: .job)
        items = try c.decodeIfPresent([Check].self, forKey: .items) ?? []
        tool = try c.decodeIfPresent(OffloadTool.self, forKey: .tool)
    }

    public enum CodingKeys: String, CodingKey { case status, job, items, tool }
}

/// An item the manager or the provider refused (422 `offload_refused`), from the error body. `refusal` is the
/// computer's protection rule code, set only when the manager's pre-check refused it; `reason` is a sentence in the
/// caller's language (the rule's, or the provider's own words).
public nonisolated struct OffloadRefusal: Codable, Hashable, Sendable {
    public var path: String?
    public var id: String?
    public var refusal: String?
    public var reason: String

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        path = try c.decodeIfPresent(String.self, forKey: .path)
        id = try c.decodeIfPresent(String.self, forKey: .id)
        refusal = try c.decodeIfPresent(String.self, forKey: .refusal)
        reason = try c.decodeIfPresent(String.self, forKey: .reason) ?? ""
    }

    public enum CodingKeys: String, CodingKey { case path, id, refusal, reason }
}

public nonisolated enum OffloadJobState: String, LenientWireEnum {
    case running, done, failed, canceled
    public static var fallback: Self { .running }
}

public nonisolated enum OffloadJobPhase: String, LenientWireEnum {
    case install, check, upload, verify, cleanup, download, other
    public static var fallback: Self { .other }
}

/// `GET …/offload/{cid}/jobs/{job}?machine=`: how far a run got.
public nonisolated struct OffloadJob: Codable, Hashable, Sendable {
    public struct Item: Codable, Hashable, Sendable {
        public var path: String?
        public var id: String?
        public var status: String
        public var error: String?
        public var bytes: Int64?
    }

    public var state: OffloadJobState
    public var phase: OffloadJobPhase?
    public var doneBytes: Int64
    public var totalBytes: Int64
    public var freedBytes: Int64?
    public var error: String?
    public var items: [Item]

    public enum CodingKeys: String, CodingKey {
        case state, phase, error, items
        case doneBytes = "done_bytes"
        case totalBytes = "total_bytes"
        case freedBytes = "freed_bytes"
    }

    public init(state: OffloadJobState, phase: OffloadJobPhase? = nil, doneBytes: Int64 = 0, totalBytes: Int64 = 0, freedBytes: Int64? = nil,
                error: String? = nil, items: [Item] = []) {
        self.state = state
        self.phase = phase
        self.doneBytes = doneBytes
        self.totalBytes = totalBytes
        self.freedBytes = freedBytes
        self.error = error
        self.items = items
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        state = try c.decodeIfPresent(OffloadJobState.self, forKey: .state) ?? .running
        phase = try c.decodeIfPresent(OffloadJobPhase.self, forKey: .phase)
        doneBytes = try c.decodeIfPresent(Int64.self, forKey: .doneBytes) ?? 0
        totalBytes = try c.decodeIfPresent(Int64.self, forKey: .totalBytes) ?? 0
        freedBytes = try c.decodeIfPresent(Int64.self, forKey: .freedBytes)
        error = try c.decodeIfPresent(String.self, forKey: .error)
        items = try c.decodeIfPresent([Item].self, forKey: .items) ?? []
    }

    /// 0…1, for the tray's bar.
    public var fraction: Double { totalBytes > 0 ? min(1, Double(doneBytes) / Double(totalBytes)) : 0 }
    public var finished: Bool { state != .running }
}
