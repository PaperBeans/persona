package proto

// A computer's disk usage map (#14576, docs/server/machine-usage-map.md): the node sizes its disk as a tree the apps
// draw as a treemap, moves items picked on the map to the trash (and back, and out of it for good), and checks paths
// against its protection rules before an extension is handed them. Sizes are allocated bytes; every path is checked by
// the node again, on the real file, every time.

// The node methods (capability nodeproto.MachineStorageUsage). None is on node.MachineOps, the ops command line or the
// MCP server.
const (
	MethodMachineStorageUsage   = "machine.storage.usage"   // StorageUsageParams → StorageUsageReport
	MethodMachineStorageTrash   = "machine.storage.trash"   // StorageTrashParams → StorageTrashResult
	MethodMachineStorageUntrash = "machine.storage.untrash" // StorageUntrashParams → StorageUntrashResult
	MethodMachineStoragePurge   = "machine.storage.purge"   // StoragePurgeParams → StoragePurgeResult
	MethodMachineStorageCheck   = "machine.storage.check"   // StorageCheckParams → StorageCheckResult
)

// Limits of the usage map's calls, the same at the node and the manager.
const (
	// StorageUsageWaitDefaultMS and StorageUsageWaitMostMS bound how long machine.storage.usage waits for its scan.
	StorageUsageWaitDefaultMS = 1500
	StorageUsageWaitMostMS    = 10000
	// StoragePathsMost is the most paths (or trash ids) one trash, untrash, purge or check names.
	StoragePathsMost = 50
)

// Scan states of a StorageUsageReport.
const (
	StorageUsageScanning = "scanning" // the scan goes on; ask again (about once a second) for more
	StorageUsageReady    = "ready"
)

// StorageUsageNode kinds.
const (
	UsageKindRoot       = "root"
	UsageKindGroup      = "group"
	UsageKindDir        = "dir"
	UsageKindFile       = "file"
	UsageKindApp        = "app"
	UsageKindMount      = "mount" // another filesystem's root: its statfs numbers, opened as a path of its own
	UsageKindOther      = "other" // the children past the listed ones, summed (Items says how many)
	UsageKindFree       = "free"
	UsageKindUnmeasured = "unmeasured" // used − measured, never below 0
)

// StorageUsageNode keys: the fixed boxes the clients name themselves.
const (
	UsageKeyApps       = "apps"
	UsageKeyHome       = "home"
	UsageKeyAppData    = "appdata"
	UsageKeySystem     = "system"
	UsageKeyFree       = "free"
	UsageKeyOther      = "other"
	UsageKeyUnmeasured = "unmeasured"
	UsageKeyBundle     = "bundle"
	UsageKeyCache      = "cache"
	UsageKeyData       = "data"
)

// StorageUsageNode categories: what a box is colored by.
const (
	UsageCategoryApps     = "apps"
	UsageCategoryPersonal = "personal"
	UsageCategoryAppData  = "appdata"
	UsageCategoryCache    = "cache"
	UsageCategorySystem   = "system"
	UsageCategoryFree     = "free"
	UsageCategoryOther    = "other"
)

// Purposes of machine.storage.check.
const (
	StoragePurposeTrash   = "trash"
	StoragePurposeOffload = "offload"
)

// The protection rules' refusal codes (docs/server/machine-usage-map.md "Protection rules"), a closed list.
const (
	RefusalNotFound       = "not_found"
	RefusalNotRegular     = "not_regular"
	RefusalSymlink        = "symlink"
	RefusalOutsideHome    = "outside_home"
	RefusalHomeRoot       = "home_root"
	RefusalStandardFolder = "standard_folder"
	RefusalAppManaged     = "app_managed"
	RefusalSystemCache    = "system_cache"
	RefusalDotFolder      = "dot_folder"
	RefusalCredentials    = "credentials"
	RefusalAgentData      = "agent_data"
	RefusalPersonaData    = "persona_data"
	RefusalInsideApp      = "inside_app"
	RefusalAppBundle      = "app_bundle"
	RefusalManagedLibrary = "managed_library"
	RefusalCloudSynced    = "cloud_synced"
	RefusalMountPoint     = "mount_point"
)

// StorageRefusals is every refusal code, in the order the doc's table lists them.
var StorageRefusals = []string{RefusalNotFound, RefusalNotRegular, RefusalSymlink, RefusalOutsideHome, RefusalHomeRoot,
	RefusalStandardFolder, RefusalAppManaged, RefusalSystemCache, RefusalDotFolder, RefusalCredentials, RefusalAgentData,
	RefusalPersonaData, RefusalInsideApp, RefusalAppBundle, RefusalManagedLibrary, RefusalCloudSynced, RefusalMountPoint}

// Trash item outcomes.
const (
	StorageTrashPreview = "preview"
	StorageTrashTrashed = "trashed"
	StorageTrashRefused = "refused"
	StorageTrashMissing = "missing"
	StorageTrashFailed  = "failed"
)

// Purge item outcomes.
const (
	StoragePurgePurged      = "purged"
	StoragePurgeMissing     = "missing"
	StoragePurgeFailed      = "failed"
	StoragePurgeUnsupported = "unsupported" // Windows, version 1
)

// CodeConfirmationRequired: a trash or purge (or an offload run) without dry_run or confirm (400).
const CodeConfirmationRequired = "confirmation_required"

// StorageUsageParams is machine.storage.usage's params.
type StorageUsageParams struct {
	// Path is "" for the overview, or one folder: absolute, "~" or "~/…".
	Path   string `json:"path,omitempty"`
	Rescan bool   `json:"rescan,omitempty"` // drop Path and everything under it from the cache first
	WaitMS int    `json:"wait_ms,omitempty"`
}

// StorageUsageVolume is the volume a report is about.
type StorageUsageVolume struct {
	Mount      string `json:"mount"`
	TotalBytes int64  `json:"total_bytes"`
	UsedBytes  int64  `json:"used_bytes"`
	FreeBytes  int64  `json:"free_bytes"`
}

// StorageUsageReport is machine.storage.usage's answer.
type StorageUsageReport struct {
	MachineID  string             `json:"machine_id,omitempty"` // set by the manager
	At         int64              `json:"at"`                   // Unix seconds
	OS         string             `json:"os"`
	Path       string             `json:"path"` // "" (the overview) or the absolute folder
	State      string             `json:"state"`
	Partial    bool               `json:"partial"`
	Cached     bool               `json:"cached"`
	ScannedAt  int64              `json:"scanned_at"`
	DurationMS int64              `json:"duration_ms"`
	Home       string             `json:"home"`
	Volume     StorageUsageVolume `json:"volume"`
	Node       StorageUsageNode   `json:"node"`
}

// StorageUsageApp is what an app node says of its bundle.
type StorageUsageApp struct {
	BundleID string `json:"bundle_id"`
	Version  string `json:"version,omitempty"`
}

// StorageUsageNode is one box of the map. The booleans are always written.
type StorageUsageNode struct {
	ID       string `json:"id"` // the absolute path, or "group:<key>" for a box that is no one path
	Name     string `json:"name"`
	Path     string `json:"path,omitempty"`
	Display  string `json:"display,omitempty"` // "~/…" inside home
	Kind     string `json:"kind"`
	Key      string `json:"key,omitempty"`
	Category string `json:"category"`
	Bytes    int64  `json:"bytes"`
	Items    int    `json:"items,omitempty"`   // on an "other" node: how many children it sums
	UsedAt   int64  `json:"used_at,omitempty"` // newest modification time in the subtree, Unix seconds
	// HasChildren: the box can be opened (a folder, a group); Children are what is known of them now.
	HasChildren bool               `json:"has_children"`
	Partial     bool               `json:"partial"`
	Children    []StorageUsageNode `json:"children,omitempty"`
	CanTrash    bool               `json:"can_trash"`
	CanOffload  bool               `json:"can_offload"`
	Refusal     string             `json:"refusal,omitempty"`  // the first rule that refuses trash
	CacheID     string             `json:"cache_id,omitempty"` // one of CacheIDs: offer its Clean flow instead of Trash
	CacheInUse  bool               `json:"cache_in_use,omitempty"`
	App         *StorageUsageApp   `json:"app,omitempty"`
}

// StorageTrashParams is machine.storage.trash's params: absolute paths, and dry_run or confirm.
type StorageTrashParams struct {
	Paths   []string `json:"paths"`
	DryRun  bool     `json:"dry_run,omitempty"`
	Confirm bool     `json:"confirm,omitempty"`
}

// StorageTrashItem is one path's outcome.
type StorageTrashItem struct {
	Path    string `json:"path"`
	Display string `json:"display"`
	Bytes   int64  `json:"bytes"`
	Status  string `json:"status"` // StorageTrash*
	Refusal string `json:"refusal,omitempty"`
	Error   string `json:"error,omitempty"`
	TrashID string `json:"trash_id,omitempty"`
}

// StorageTrashResult is what a trash did. Moving to the trash frees nothing until it is emptied: PendingBytes is
// what went there, FreedBytes what the volume measured (usually 0).
type StorageTrashResult struct {
	MachineID       string             `json:"machine_id,omitempty"` // set by the manager
	DryRun          bool               `json:"dry_run"`
	FreedBytes      int64              `json:"freed_bytes"`
	PendingBytes    int64              `json:"pending_bytes"`
	AvailableBefore int64              `json:"available_before"`
	AvailableAfter  int64              `json:"available_after"`
	Items           []StorageTrashItem `json:"items"`
}

// StorageUntrashParams is machine.storage.untrash's params.
type StorageUntrashParams struct {
	TrashIDs []string `json:"trash_ids"`
}

// StorageUntrashResult is what was put back.
type StorageUntrashResult struct {
	MachineID string                `json:"machine_id,omitempty"`
	Restored  []StorageRestoredItem `json:"restored"`
}

// StorageRestoredItem is one item put back.
type StorageRestoredItem struct {
	Path string `json:"path"`
}

// StoragePurgeParams is machine.storage.purge's params.
type StoragePurgeParams struct {
	TrashIDs []string `json:"trash_ids"`
	Confirm  bool     `json:"confirm,omitempty"`
}

// StoragePurgeItem is one trash id's outcome.
type StoragePurgeItem struct {
	TrashID string `json:"trash_id"`
	Status  string `json:"status"` // StoragePurge*
	Error   string `json:"error,omitempty"`
}

// StoragePurgeResult is what a purge did, measured on the home folder's volume.
type StoragePurgeResult struct {
	MachineID       string             `json:"machine_id,omitempty"`
	FreedBytes      int64              `json:"freed_bytes"`
	AvailableBefore int64              `json:"available_before"`
	AvailableAfter  int64              `json:"available_after"`
	Items           []StoragePurgeItem `json:"items"`
}

// StorageCheckParams is machine.storage.check's params.
type StorageCheckParams struct {
	Paths   []string `json:"paths"`
	Purpose string   `json:"purpose"` // StoragePurpose*
}

// StorageCheckItem is one path's answer.
type StorageCheckItem struct {
	Path    string `json:"path"`
	OK      bool   `json:"ok"`
	Refusal string `json:"refusal,omitempty"`
	Kind    string `json:"kind"`            // UsageKindFile, UsageKindDir, UsageKindApp, or "" when it is not there
	Bytes   int64  `json:"bytes,omitempty"` // when the node already knows it
}

// StorageCheckResult is machine.storage.check's answer.
type StorageCheckResult struct {
	Items []StorageCheckItem `json:"items"`
}
