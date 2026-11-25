package node

// The disk usage map's node methods (#14576, docs/server/machine-usage-map.md): machine.storage.usage sizes the disk as a
// tree, machine.storage.trash moves items picked on it to the trash after the protection rules agreed again,
// machine.storage.untrash puts them back, machine.storage.purge deletes for good only what this flow put in the trash,
// and machine.storage.check answers the rules for paths an offload provider is about to be handed. The scan and the
// rules are package diskmap; the trash is the file browser's own (toTrash, restoreOne in fileedit.go). None of these is
// on MachineOps, the ops command line or the MCP server.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"syscall"
	"time"

	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/node/diskmap"
	"github.com/nwverse/persona/server/internal/node/machdiag"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

func init() {
	nodeRPC(proto.MethodMachineStorageUsage, (*Node).storageUsage)
	nodeRPC(proto.MethodMachineStorageTrash, (*Node).storageTrash)
	nodeRPC(proto.MethodMachineStorageUntrash, (*Node).storageUntrash)
	nodeRPC(proto.MethodMachineStoragePurge, (*Node).storagePurge)
	nodeRPC(proto.MethodMachineStorageCheck, (*Node).storageCheck)
}

// trashRecordMost is how many items this flow's trash remembers for a purge; the oldest are forgotten first.
const trashRecordMost = 2000

// trashSizingMost bounds the time one trash call spends sizing folders the map has not measured, across all its
// paths, so 50 unmeasured folders still answer well inside the manager's 60 s (an answer that comes too late carries
// no trash ids, and so no Undo). Past it an item's bytes are what was measured, or 0.
const trashSizingMost = 20 * time.Second

// usageMapState is the node's usage map and what its trash put in the trash: only those may be purged. The record is
// memory only, so after the node restarts a purge answers missing and the user empties the trash themselves.
type usageMapState struct {
	once sync.Once
	m    *diskmap.Map

	mu      sync.Mutex
	trashed map[string]trashedItem
	order   []string
}

type trashedItem struct {
	real  string
	bytes int64
}

// diskMap is the node's usage map, made on first use.
func (n *Node) diskMap() *diskmap.Map {
	s := &n.usageMap
	s.once.Do(func() {
		var agents []string
		for _, v := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME"} {
			if d := os.Getenv(v); filepath.IsAbs(d) {
				agents = append(agents, d)
			}
		}
		persona := []string{n.dir}
		if n.home != "" {
			persona = append(persona, identity.DataDir(n.home))
		}
		s.m = diskmap.New(diskmap.Options{Home: n.home, Caches: machdiag.CacheFolders(runtime.GOOS, n.home, os.Getenv),
			CachesInUse: machdiag.CachesInUse, AgentDirs: agents, PersonaDirs: persona})
	})
	return s.m
}

func storageBadRequest(msg string) error { return &rpc.Error{Code: "bad_request", Message: msg} }

// storageUsage is machine.storage.usage.
func (n *Node) storageUsage(ctx context.Context, p proto.StorageUsageParams) (proto.StorageUsageReport, error) {
	if n.home == "" {
		return proto.StorageUsageReport{}, errors.New("no home folder")
	}
	r, err := n.diskMap().Usage(ctx, p)
	if errors.Is(err, diskmap.ErrBadPath) {
		return r, storageBadRequest("not a folder the map opens")
	}
	return r, err
}

// storageCheck is machine.storage.check.
func (n *Node) storageCheck(_ context.Context, p proto.StorageCheckParams) (proto.StorageCheckResult, error) {
	if len(p.Paths) == 0 || len(p.Paths) > proto.StoragePathsMost {
		return proto.StorageCheckResult{}, storageBadRequest(fmt.Sprintf("1 to %d paths", proto.StoragePathsMost))
	}
	if p.Purpose != proto.StoragePurposeTrash && p.Purpose != proto.StoragePurposeOffload {
		return proto.StorageCheckResult{}, storageBadRequest("purpose is trash or offload")
	}
	items := n.diskMap().Check(p.Paths, p.Purpose)
	for i := range items {
		if items[i].OK && n.offLimitsResolved(items[i].Path) != nil {
			items[i].OK, items[i].Refusal = false, proto.RefusalPersonaData
		}
	}
	return proto.StorageCheckResult{Items: items}, nil
}

// storageTrash is machine.storage.trash: every path is checked again and moved on its own; one refusal undoes nothing.
func (n *Node) storageTrash(_ context.Context, p proto.StorageTrashParams) (proto.StorageTrashResult, error) {
	if len(p.Paths) == 0 || len(p.Paths) > proto.StoragePathsMost {
		return proto.StorageTrashResult{}, storageBadRequest(fmt.Sprintf("1 to %d paths", proto.StoragePathsMost))
	}
	if !p.DryRun && !p.Confirm {
		return proto.StorageTrashResult{}, &rpc.Error{Code: proto.CodeConfirmationRequired, Message: "a trash needs dry_run or confirm"}
	}
	if n.home == "" {
		return proto.StorageTrashResult{}, errors.New("no home folder")
	}
	m := n.diskMap()
	out := proto.StorageTrashResult{DryRun: p.DryRun, Items: []proto.StorageTrashItem{}}
	out.AvailableBefore = n.volumeFree(m)
	var seen []string
	trashed := 0
	sizeBy := time.Now().Add(trashSizingMost)
	for _, path := range p.Paths {
		it := n.trashOne(m, path, p.DryRun, &seen, time.Until(sizeBy))
		if it.Status == proto.StorageTrashTrashed {
			out.PendingBytes += it.Bytes
			trashed++
		}
		out.Items = append(out.Items, it)
	}
	out.AvailableAfter = n.volumeFree(m)
	out.FreedBytes = max(out.AvailableAfter-out.AvailableBefore, 0)
	if !p.DryRun {
		n.logger.Printf("usage map: moved %d of %d items to the trash (%d bytes)", trashed, len(p.Paths), out.PendingBytes)
	}
	return out, nil
}

// volumeFree is the home volume's free bytes, read now.
func (n *Node) volumeFree(m *diskmap.Map) int64 { return m.Free() }

// trashOne checks and (unless dryRun) trashes one path, sizing it within sizeFor.
func (n *Node) trashOne(m *diskmap.Map, path string, dryRun bool, seen *[]string, sizeFor time.Duration) proto.StorageTrashItem {
	it := proto.StorageTrashItem{Path: path, Display: path}
	real, err := m.Resolve(path)
	if err != nil {
		it.Status, it.Refusal = proto.StorageTrashRefused, proto.RefusalNotFound
		return it
	}
	it.Path, it.Display = real, m.Display(real)
	if slices.Contains(*seen, real) {
		it.Status, it.Error = proto.StorageTrashFailed, "named twice"
		return it
	}
	*seen = append(*seen, real)
	refusal, _ := m.Refusal(real, proto.StoragePurposeTrash)
	if refusal == "" && n.offLimitsResolved(real) != nil {
		refusal = proto.RefusalPersonaData
	}
	switch refusal {
	case "":
	case proto.RefusalNotFound:
		it.Status = proto.StorageTrashMissing
		return it
	default:
		it.Status, it.Refusal = proto.StorageTrashRefused, refusal
		return it
	}
	it.Bytes, _ = m.Size(real, sizeFor)
	if dryRun {
		it.Status = proto.StorageTrashPreview
		return it
	}
	id, err := n.toTrash(real, it.Display)
	if err != nil {
		if errors.Is(err, syscall.ENAMETOOLONG) {
			err = fmt.Errorf("%s has a name too long for the trash", it.Display)
		}
		it.Status, it.Error = proto.StorageTrashFailed, err.Error()
		return it
	}
	it.Status, it.TrashID = proto.StorageTrashTrashed, id
	m.Removed(real, it.Bytes)
	n.rememberTrash(id, real, it.Bytes)
	return it
}

// rememberTrash records an id this flow put in the trash, for Undo's map update and a purge.
func (n *Node) rememberTrash(id, real string, bytes int64) {
	s := &n.usageMap
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.trashed == nil {
		s.trashed = map[string]trashedItem{}
	}
	if _, ok := s.trashed[id]; !ok {
		s.order = append(s.order, id)
	}
	s.trashed[id] = trashedItem{real: real, bytes: bytes}
	for len(s.order) > trashRecordMost {
		delete(s.trashed, s.order[0])
		s.order = s.order[1:]
	}
}

// takeTrash forgets id, saying whether this flow had put it in the trash.
func (n *Node) takeTrash(id string) (trashedItem, bool) {
	s := &n.usageMap
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.trashed[id]
	if ok {
		delete(s.trashed, id)
		s.order = slices.DeleteFunc(s.order, func(x string) bool { return x == id })
	}
	return t, ok
}

// storageUntrash is machine.storage.untrash: each item goes back where it was, refused where that place is taken.
func (n *Node) storageUntrash(_ context.Context, p proto.StorageUntrashParams) (proto.StorageUntrashResult, error) {
	if len(p.TrashIDs) == 0 || len(p.TrashIDs) > proto.StoragePathsMost {
		return proto.StorageUntrashResult{}, storageBadRequest(fmt.Sprintf("1 to %d trash_ids", proto.StoragePathsMost))
	}
	files, info, err := n.trashDirs()
	if err != nil {
		return proto.StorageUntrashResult{}, err
	}
	out := proto.StorageUntrashResult{Restored: []proto.StorageRestoredItem{}}
	for _, s := range p.TrashIDs {
		t, err := decodeTrashID(s)
		if err != nil {
			return out, storageBadRequest("bad trash_id")
		}
		if err := n.restoreOne(files, info, t); err != nil {
			return out, err
		}
		rec, ok := n.takeTrash(s)
		if ok {
			n.diskMap().Restored(t.Real, rec.bytes)
		}
		out.Restored = append(out.Restored, proto.StorageRestoredItem{Path: t.Shown})
	}
	n.logger.Printf("usage map: put back %d items from the trash", len(out.Restored))
	return out, nil
}

// storagePurge is machine.storage.purge: what this flow put in the trash is deleted for good, measured on the volume.
func (n *Node) storagePurge(_ context.Context, p proto.StoragePurgeParams) (proto.StoragePurgeResult, error) {
	if len(p.TrashIDs) == 0 || len(p.TrashIDs) > proto.StoragePathsMost {
		return proto.StoragePurgeResult{}, storageBadRequest(fmt.Sprintf("1 to %d trash_ids", proto.StoragePathsMost))
	}
	if !p.Confirm {
		return proto.StoragePurgeResult{}, &rpc.Error{Code: proto.CodeConfirmationRequired, Message: "a purge needs confirm"}
	}
	m := n.diskMap()
	out := proto.StoragePurgeResult{Items: []proto.StoragePurgeItem{}}
	out.AvailableBefore = n.volumeFree(m)
	purged := 0
	for _, id := range p.TrashIDs {
		it := n.purgeOne(id)
		if it.Status == proto.StoragePurgePurged {
			purged++
		}
		out.Items = append(out.Items, it)
	}
	out.AvailableAfter = n.volumeFree(m)
	out.FreedBytes = max(out.AvailableAfter-out.AvailableBefore, 0)
	n.logger.Printf("usage map: deleted %d of %d items from the trash for good (%d bytes freed)", purged, len(p.TrashIDs), out.FreedBytes)
	return out, nil
}

// purgeOne deletes one trashed item for good: only one this flow put there, under a plain name in the node's own
// trash folder.
func (n *Node) purgeOne(id string) proto.StoragePurgeItem {
	it := proto.StoragePurgeItem{TrashID: id, Status: proto.StoragePurgeMissing}
	if runtime.GOOS == "windows" {
		it.Status = proto.StoragePurgeUnsupported
		return it
	}
	t, err := decodeTrashID(id)
	if err != nil {
		return it
	}
	files, info, err := n.trashDirs()
	if err != nil || files == "" {
		it.Status, it.Error = proto.StoragePurgeUnsupported, "no trash folder"
		return it
	}
	if _, ok := n.takeTrash(id); !ok {
		return it // not put there by this flow (or before the node restarted): the user empties the trash
	}
	at := filepath.Join(files, t.Name)
	if filepath.Dir(at) != filepath.Clean(files) {
		return it
	}
	if _, err := os.Lstat(at); err != nil {
		return it
	}
	if err := os.RemoveAll(at); err != nil {
		it.Status, it.Error = proto.StoragePurgeFailed, err.Error()
		return it
	}
	if info != "" {
		_ = os.Remove(filepath.Join(info, t.Name+".trashinfo"))
	}
	it.Status = proto.StoragePurgePurged
	return it
}
