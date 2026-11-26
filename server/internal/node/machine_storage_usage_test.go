package node

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

func storageCall[R any](t *testing.T, n *Node, method string, params any) (R, error) {
	t.Helper()
	raw, _ := json.Marshal(params)
	var zero R
	out, err := n.handle(context.Background(), method, raw)
	if err != nil {
		return zero, err
	}
	r, ok := out.(R)
	if !ok {
		t.Fatalf("%s answered %T", method, out)
	}
	return r, nil
}

func usageErrCode(err error) string {
	var re *rpc.Error
	if errors.As(err, &re) {
		return re.Code
	}
	return ""
}

// The five methods reach the usage map, and refuse what is malformed before touching anything.
func TestStorageUsageDispatch(t *testing.T) {
	n, home := writeNode(t)
	makeFile(t, filepath.Join(home, "proj", "a.txt"), "a")
	for _, c := range []struct {
		method string
		params any
		code   string
	}{
		{proto.MethodMachineStorageUsage, map[string]any{"path": "relative"}, "bad_request"},
		{proto.MethodMachineStorageTrash, map[string]any{"paths": []string{}}, "bad_request"},
		{proto.MethodMachineStorageTrash, map[string]any{"paths": []string{"~/proj/a.txt"}}, proto.CodeConfirmationRequired},
		{proto.MethodMachineStorageUntrash, map[string]any{"trash_ids": []string{}}, "bad_request"},
		{proto.MethodMachineStorageUntrash, map[string]any{"trash_ids": []string{"not-an-id"}}, "bad_request"},
		{proto.MethodMachineStoragePurge, map[string]any{"trash_ids": []string{"x"}}, proto.CodeConfirmationRequired},
		{proto.MethodMachineStorageCheck, map[string]any{"paths": []string{"~/proj"}, "purpose": "delete"}, "bad_request"},
		{proto.MethodMachineStorageCheck, map[string]any{"paths": make([]string, proto.StoragePathsMost+1), "purpose": "trash"}, "bad_request"},
	} {
		raw, _ := json.Marshal(c.params)
		if _, err := n.handle(context.Background(), c.method, raw); usageErrCode(err) != c.code {
			t.Errorf("%s %s: %v, want %s", c.method, raw, err, c.code)
		}
	}
	if _, err := storageCall[proto.StorageUsageReport](t, n, proto.MethodMachineStorageUsage, proto.StorageUsageParams{Path: "~/proj", WaitMS: 5000}); err != nil {
		t.Fatal(err)
	}
	check, err := storageCall[proto.StorageCheckResult](t, n, proto.MethodMachineStorageCheck, proto.StorageCheckParams{
		Paths: []string{"~/proj/a.txt", "~/.ssh/id_rsa"}, Purpose: proto.StoragePurposeOffload})
	if err != nil || len(check.Items) != 2 || !check.Items[0].OK || check.Items[1].OK {
		t.Fatalf("check %+v %v", check, err)
	}
}

// Trash, Undo and Delete Permanently in a temporary home: each item on its own, the map updated in place, and a purge
// only of what this flow put in the trash.
func TestStorageTrashUndoPurge(t *testing.T) {
	skipOnRecycleBin(t)
	t.Setenv("XDG_DATA_HOME", "")
	n, home := writeNode(t)
	makeFile(t, filepath.Join(home, "proj", "big.bin"), string(make([]byte, 64<<10)))
	makeFile(t, filepath.Join(home, "proj", "keep.txt"), "keep")
	makeFile(t, filepath.Join(home, "proj", "old", "x.bin"), string(make([]byte, 32<<10)))
	r, err := storageCall[proto.StorageUsageReport](t, n, proto.MethodMachineStorageUsage, proto.StorageUsageParams{Path: "~/proj", WaitMS: 5000})
	if err != nil || r.State != proto.StorageUsageReady {
		t.Fatalf("usage %+v %v", r, err)
	}
	projBytes := r.Node.Bytes

	paths := []string{"~/proj/big.bin", "~/.ssh", "~/proj/gone", "~/proj/old"}
	preview, err := storageCall[proto.StorageTrashResult](t, n, proto.MethodMachineStorageTrash, proto.StorageTrashParams{Paths: paths, DryRun: true})
	if err != nil || !preview.DryRun || preview.Items[0].Status != proto.StorageTrashPreview || preview.Items[0].Bytes <= 0 {
		t.Fatalf("preview %+v %v", preview, err)
	}
	if _, err := os.Lstat(filepath.Join(home, "proj", "big.bin")); err != nil {
		t.Fatal("a dry run moved something")
	}
	res, err := storageCall[proto.StorageTrashResult](t, n, proto.MethodMachineStorageTrash, proto.StorageTrashParams{Paths: paths, Confirm: true})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{proto.StorageTrashTrashed, proto.StorageTrashMissing, proto.StorageTrashMissing, proto.StorageTrashTrashed}
	for i, it := range res.Items {
		if it.Status != want[i] {
			t.Errorf("%s: %s (%s %s), want %s", it.Path, it.Status, it.Refusal, it.Error, want[i])
		}
	}
	if res.Items[0].TrashID == "" || res.Items[0].Display != "~/proj/big.bin" || res.PendingBytes != res.Items[0].Bytes+res.Items[3].Bytes {
		t.Errorf("result %+v", res)
	}
	notThere(t, filepath.Join(home, "proj", "big.bin"))
	// The map knows without a rescan.
	r2, _ := storageCall[proto.StorageUsageReport](t, n, proto.MethodMachineStorageUsage, proto.StorageUsageParams{Path: "~/proj", WaitMS: 5000})
	if !r2.Cached || r2.Node.Bytes != projBytes-res.PendingBytes {
		t.Errorf("after the trash: cached %v, %d bytes, want %d", r2.Cached, r2.Node.Bytes, projBytes-res.PendingBytes)
	}

	// Undo puts one back.
	back, err := storageCall[proto.StorageUntrashResult](t, n, proto.MethodMachineStorageUntrash, proto.StorageUntrashParams{TrashIDs: []string{res.Items[3].TrashID}})
	if err != nil || len(back.Restored) != 1 {
		t.Fatalf("untrash %+v %v", back, err)
	}
	if _, err := os.Lstat(filepath.Join(home, "proj", "old", "x.bin")); err != nil {
		t.Fatal("undo did not put the folder back")
	}

	// A made-up id, and one this flow did not issue, are never deleted.
	files, _, _ := n.trashDirs()
	makeFile(t, filepath.Join(files, "someone-elses.txt"), "theirs")
	forged := trashID{Name: "someone-elses.txt", Real: filepath.Join(home, "x"), Shown: "~/x"}.encode()
	purge, err := storageCall[proto.StoragePurgeResult](t, n, proto.MethodMachineStoragePurge, proto.StoragePurgeParams{
		TrashIDs: []string{forged, "garbage", res.Items[0].TrashID, res.Items[3].TrashID}, Confirm: true})
	if err != nil {
		t.Fatal(err)
	}
	wantPurge := []string{proto.StoragePurgeMissing, proto.StoragePurgeMissing, proto.StoragePurgePurged, proto.StoragePurgeMissing}
	for i, it := range purge.Items {
		if it.Status != wantPurge[i] {
			t.Errorf("purge %d: %s, want %s", i, it.Status, wantPurge[i])
		}
	}
	if fileText(t, filepath.Join(files, "someone-elses.txt")) != "theirs" {
		t.Error("a forged id deleted someone else's file")
	}
	notThere(t, filepath.Join(files, "big.bin"))
}

func TestStorageTrashRefusesProtected(t *testing.T) {
	skipOnRecycleBin(t)
	n, home := writeNode(t)
	makeFile(t, filepath.Join(home, ".ssh", "id_ed25519"), "key")
	makeFile(t, filepath.Join(home, ".claude", "settings.json"), "{}")
	if err := os.MkdirAll(filepath.Join(home, "Documents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, "proj"), filepath.Join(home, "proj-link")); err != nil {
		t.Fatal(err)
	}
	res, err := storageCall[proto.StorageTrashResult](t, n, proto.MethodMachineStorageTrash, proto.StorageTrashParams{
		Paths: []string{"~/.ssh/id_ed25519", "~/.claude", "~/Documents", "~/proj-link", "~", "/etc/hosts"}, Confirm: true})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{proto.RefusalCredentials, proto.RefusalAgentData, proto.RefusalStandardFolder, proto.RefusalSymlink,
		proto.RefusalHomeRoot, proto.RefusalOutsideHome}
	if runtime.GOOS == "darwin" {
		want[5] = proto.RefusalOutsideHome
	}
	for i, it := range res.Items {
		if it.Status != proto.StorageTrashRefused || it.Refusal != want[i] {
			t.Errorf("%s: %s %s, want refused %s", it.Path, it.Status, it.Refusal, want[i])
		}
	}
	if res.PendingBytes != 0 {
		t.Errorf("pending %d", res.PendingBytes)
	}
}
