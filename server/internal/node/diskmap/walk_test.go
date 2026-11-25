//go:build linux || darwin

package diskmap

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// write makes a file of n bytes (and its folders).
func write(t *testing.T, path string, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, n), 0o644); err != nil {
		t.Fatal(err)
	}
}

// allocated is what the walker counts for path.
func allocated(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := statOf(info)
	return st.bytes
}

func testMap(t *testing.T, home string) *Map {
	t.Helper()
	return New(Options{GOOS: "linux", Home: home, Walkers: 2, SystemRoots: []string{}, AppRoots: []string{},
		Volume: func(string) (int64, int64, int64, error) { return 1 << 40, 1 << 39, 1 << 39, nil }})
}

func usage(t *testing.T, m *Map, path string) proto.StorageUsageReport {
	t.Helper()
	r, err := m.Usage(context.Background(), proto.StorageUsageParams{Path: path, WaitMS: proto.StorageUsageWaitMostMS})
	if err != nil {
		t.Fatal(err)
	}
	if r.State != proto.StorageUsageReady {
		t.Fatalf("%s: state %s after %d ms", path, r.State, proto.StorageUsageWaitMostMS)
	}
	return r
}

func child(n proto.StorageUsageNode, name string) *proto.StorageUsageNode {
	for i := range n.Children {
		if n.Children[i].Name == name {
			return &n.Children[i]
		}
	}
	return nil
}

func TestWalkSizesLinksAndPrefetch(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "proj")
	write(t, filepath.Join(root, "big", "a.bin"), 300_000)
	write(t, filepath.Join(root, "big", "deep", "b.bin"), 100_000)
	write(t, filepath.Join(root, "big", "empty.bin"), 0)
	write(t, filepath.Join(root, "small.txt"), 10)
	// A hard link counts once; a symbolic link is never followed.
	if err := os.Link(filepath.Join(root, "big", "a.bin"), filepath.Join(root, "big", "a-again.bin")); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	write(t, filepath.Join(outside, "huge.bin"), 900_000)
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	m := testMap(t, home)
	r := usage(t, m, root)
	if r.Path != mustReal(t, root) || r.Node.Kind != proto.UsageKindDir || r.Cached {
		t.Fatalf("report %+v", r)
	}
	big := child(r.Node, "big")
	if big == nil {
		t.Fatalf("no big in %+v", r.Node.Children)
	}
	want := allocated(t, filepath.Join(root, "big", "a.bin")) + allocated(t, filepath.Join(root, "big", "deep", "b.bin"))
	if big.Bytes < want || big.Bytes > want+64<<10 { // the folders' own blocks on top
		t.Errorf("big = %d, want about %d (the hard link once)", big.Bytes, want)
	}
	if child(r.Node, "link") != nil {
		t.Error("a symbolic link was listed")
	}
	if r.Node.Bytes > 500_000+64<<10 {
		t.Errorf("root %d bytes: the link's target was counted", r.Node.Bytes)
	}
	// Prefetch: big carries all its own children, and opening it is answered from the cache.
	if child(*big, "deep") == nil || child(*big, "a.bin") == nil || child(*big, "a-again.bin") == nil || child(*big, "empty.bin") == nil {
		t.Errorf("big's children not prefetched: %+v", big.Children)
	}
	if big.UsedAt == 0 || !big.HasChildren || !big.CanTrash {
		t.Errorf("big %+v", big)
	}
	if f := child(r.Node, "small.txt"); f == nil || f.Kind != proto.UsageKindFile || f.HasChildren {
		t.Errorf("small.txt %+v", f)
	}
	again := usage(t, m, filepath.Join(root, "big"))
	if !again.Cached || child(again.Node, "deep") == nil {
		t.Errorf("opening a prefetched child: cached %v, %+v", again.Cached, again.Node.Children)
	}
}

func TestItemNodesKeepZeroFilesAndHideAdjustedZero(t *testing.T) {
	home := t.TempDir()
	adjusted := filepath.Join(home, "adjusted")
	b := testMap(t, home).builder(context.Background(), nil)
	b.adjust[adjusted] = adjustment{minus: 10}
	nodes := b.itemNodes(&folder{path: home, children: []item{
		{name: "empty", path: filepath.Join(home, "empty"), kind: kindFile},
		{name: "empty-dir", path: filepath.Join(home, "empty-dir"), kind: kindDir},
		{name: "adjusted", path: adjusted, kind: kindFile, bytes: 10},
	}}, false)
	n := proto.StorageUsageNode{Children: nodes}
	if child(n, "empty") == nil {
		t.Error("a zero-byte file was hidden")
	}
	if child(n, "empty-dir") != nil {
		t.Error("a zero-byte folder was listed")
	}
	if child(n, "adjusted") != nil {
		t.Error("an item adjusted to zero was listed")
	}
}

func mustReal(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestWalkMergesOther(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "many")
	for i := range listMost + 10 {
		write(t, filepath.Join(root, fmt.Sprintf("f%03d", i)), 5000+i*10)
	}
	r := usage(t, testMap(t, home), root)
	if len(r.Node.Children) != listMost+1 {
		t.Fatalf("%d children, want %d and other", len(r.Node.Children), listMost)
	}
	other := r.Node.Children[listMost]
	if other.Kind != proto.UsageKindOther || other.Items != 10 || other.Bytes <= 0 || other.ID != "group:other:"+r.Node.ID {
		t.Errorf("other %+v", other)
	}
	var sum int64
	for _, c := range r.Node.Children {
		sum += c.Bytes
	}
	if sum > r.Node.Bytes {
		t.Errorf("children %d > folder %d", sum, r.Node.Bytes)
	}
	for i := 1; i < listMost; i++ {
		if r.Node.Children[i].Bytes > r.Node.Children[i-1].Bytes {
			t.Fatal("children not largest first")
		}
	}
}

func TestWalkPrefetchLists16(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "p")
	for i := range 30 {
		write(t, filepath.Join(root, "sub", fmt.Sprintf("f%02d", i)), 4096*(i+1))
	}
	r := usage(t, testMap(t, home), root)
	sub := child(r.Node, "sub")
	if sub == nil || len(sub.Children) != prefetchMost+1 || sub.Children[prefetchMost].Kind != proto.UsageKindOther {
		t.Fatalf("sub %+v", sub)
	}
}

func TestWalkBudgetMakesPartial(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "wide")
	for i := range 40 {
		write(t, filepath.Join(root, fmt.Sprintf("d%02d", i), "x", "y.bin"), 4096)
	}
	m := New(Options{GOOS: "linux", Home: home, Walkers: 1, Budget: 10, AppRoots: []string{}, SystemRoots: []string{}})
	r := usage(t, m, root)
	if !r.Partial || !r.Node.Partial {
		t.Errorf("a scan over its entry budget is not partial: %+v", r)
	}
}

func TestWalkDeadlineMakesPartial(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "slow")
	for i := range 200 {
		write(t, filepath.Join(root, fmt.Sprintf("d%03d", i), "a", "b", "c.bin"), 10)
	}
	m := New(Options{GOOS: "linux", Home: home, Walkers: 1, FolderDeadline: time.Nanosecond, AppRoots: []string{}, SystemRoots: []string{}})
	r := usage(t, m, root)
	if !r.Partial {
		t.Errorf("a scan past its deadline is not partial")
	}
}

func TestUsageAnswersWhileScanning(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "w")
	write(t, filepath.Join(root, "a.bin"), 10)
	m := testMap(t, home)
	gate := make(chan struct{}, scansAtOnce)
	for range scansAtOnce {
		gate <- struct{}{} // both scan slots taken: the scan waits
	}
	m.gate = gate
	r, err := m.Usage(context.Background(), proto.StorageUsageParams{Path: root, WaitMS: 20})
	if err != nil || r.State != proto.StorageUsageScanning || r.Partial {
		t.Fatalf("a queued scan: %+v, %v", r, err)
	}
	// A second call joins the same job.
	m.mu.Lock()
	j := m.jobs[mustReal(t, root)]
	m.mu.Unlock()
	if j == nil {
		t.Fatal("no job")
	}
	if got := m.start(mustReal(t, root), nil, nil, time.Second); got != j {
		t.Error("a second call started another scan")
	}
	<-gate
	r = usage(t, m, root)
	if child(r.Node, "a.bin") == nil {
		t.Errorf("after the slot freed: %+v", r.Node)
	}
}

func TestUsageRefusesPseudoAndRelative(t *testing.T) {
	m := testMap(t, t.TempDir())
	for _, p := range []string{"relative/path", "/proc/1", "/dev", filepath.Join(t.TempDir(), "missing")} {
		if _, err := m.Usage(context.Background(), proto.StorageUsageParams{Path: p}); err == nil {
			t.Errorf("%s was opened", p)
		}
	}
}

func TestUsageTildeAndRescan(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, "docs", "a.txt"), 5000)
	m := testMap(t, home)
	r := usage(t, m, "~/docs")
	if !strings.HasSuffix(r.Path, "docs") || r.Node.Display != "~/docs" {
		t.Fatalf("~/docs: %s %s", r.Path, r.Node.Display)
	}
	write(t, filepath.Join(home, "docs", "b.txt"), 50_000)
	if r := usage(t, m, "~/docs"); !r.Cached || child(r.Node, "b.txt") != nil {
		t.Errorf("the cache was not used")
	}
	r2, err := m.Usage(context.Background(), proto.StorageUsageParams{Path: "~/docs", Rescan: true, WaitMS: 10000})
	if err != nil || r2.Cached || child(r2.Node, "b.txt") == nil {
		t.Errorf("rescan did not measure again: %+v %v", r2, err)
	}
}
