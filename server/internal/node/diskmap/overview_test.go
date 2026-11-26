//go:build linux || darwin

package diskmap

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestPlistStrings(t *testing.T) {
	for _, f := range []string{"Info-xml.plist", "Info-binary.plist"} {
		vals, err := readPlistStrings(filepath.Join("testdata", f))
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if vals["CFBundleIdentifier"] != "com.example.app" || vals["CFBundleShortVersionString"] != "3.2" || vals["CFBundleName"] != "Example Ünïcode" {
			t.Errorf("%s: %v (a nested dictionary's key must not win)", f, vals)
		}
	}
	for _, bad := range [][]byte{nil, []byte("bplist00"), []byte("<plist><array/></plist>"), []byte("bplist00" + string(make([]byte, 40)))} {
		if _, err := plistStrings(bad); err == nil {
			t.Errorf("%q read as a plist", bad)
		}
	}
}

// fakeMac is a home folder, an Applications folder and a system folder in a temporary directory, laid out like a Mac.
func fakeMac(t *testing.T) (m *Map, home, apps, sys string) {
	t.Helper()
	base := t.TempDir()
	home, apps, sys = filepath.Join(base, "home"), filepath.Join(base, "Applications"), filepath.Join(base, "Library")
	write(t, filepath.Join(home, "Documents", "report.pdf"), 200_000)
	write(t, filepath.Join(home, "Movies", "trip.mov"), 400_000)
	write(t, filepath.Join(home, ".config", "tool", "settings.json"), 9_000)
	write(t, filepath.Join(home, "Library", "Caches", "com.example.app", "blob"), 120_000)
	write(t, filepath.Join(home, "Library", "Caches", "go-build", "00", "x"), 60_000)
	write(t, filepath.Join(home, "Library", "Caches", "com.other.thing", "y"), 30_000)
	write(t, filepath.Join(home, "Library", "Application Support", "Example", "db"), 80_000)
	write(t, filepath.Join(home, "Library", "Containers", "com.example.app", "Data", "z"), 50_000)
	write(t, filepath.Join(home, "Library", "Preferences", "p.plist"), 4_000)
	plist, err := os.ReadFile(filepath.Join("testdata", "Info-binary.plist"))
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(apps, "Example.app", "Contents", "MacOS", "Example"), 300_000)
	if err := os.WriteFile(filepath.Join(apps, "Example.app", "Contents", "Info.plist"), plist, 0o644); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(apps, "Utilities", "readme"), 5_000)
	write(t, filepath.Join(sys, "Fonts", "f.ttf"), 70_000)
	m = New(Options{GOOS: "darwin", Home: home, Walkers: 2, AppRoots: []string{apps}, SystemRoots: []string{sys},
		Caches: map[string]string{"go-build": filepath.Join(mustReal(t, home), "Library", "Caches", "go-build")},
		Volume: func(string) (int64, int64, int64, error) { return 100 << 20, 40 << 20, 60 << 20, nil }})
	return m, mustReal(t, home), mustReal(t, apps), mustReal(t, sys)
}

func groupOf(t *testing.T, root proto.StorageUsageNode, key string) proto.StorageUsageNode {
	t.Helper()
	for _, c := range root.Children {
		if c.Key == key {
			return c
		}
	}
	t.Fatalf("no group %s in %+v", key, root.Children)
	return proto.StorageUsageNode{}
}

func findPath(n proto.StorageUsageNode, path string) *proto.StorageUsageNode {
	if n.Path == path {
		return &n
	}
	for _, c := range n.Children {
		if f := findPath(c, path); f != nil {
			return f
		}
	}
	return nil
}

func TestOverviewGroupsAddUpAndAppsClaim(t *testing.T) {
	m, home, apps, sys := fakeMac(t)
	r := usage(t, m, "")
	if r.Path != "" || r.Node.Kind != proto.UsageKindRoot || r.Node.ID != "group:root" || r.Volume.TotalBytes != 100<<20 {
		t.Fatalf("report %+v", r)
	}
	var sum int64
	for _, g := range r.Node.Children {
		sum += g.Bytes
	}
	if sum != r.Volume.TotalBytes || r.Node.Bytes != r.Volume.TotalBytes {
		t.Errorf("groups add up to %d, root %d, volume %d", sum, r.Node.Bytes, r.Volume.TotalBytes)
	}
	if free := groupOf(t, r.Node, proto.UsageKeyFree); free.Bytes != 40<<20 || free.Kind != proto.UsageKindFree {
		t.Errorf("free %+v", free)
	}
	appsG := groupOf(t, r.Node, proto.UsageKeyApps)
	app := findPath(appsG, filepath.Join(apps, "Example.app"))
	if app == nil || app.Kind != proto.UsageKindApp || app.Name != "Example" || app.App == nil || app.App.BundleID != "com.example.app" ||
		app.App.Version != "3.2" || app.CanTrash || app.Refusal != proto.RefusalAppBundle {
		t.Fatalf("app %+v", app)
	}
	keys := map[string]int64{}
	for _, c := range app.Children {
		keys[c.Key] = c.Bytes
	}
	if keys[proto.UsageKeyBundle] == 0 || keys[proto.UsageKeyCache] == 0 || keys[proto.UsageKeyData] == 0 {
		t.Errorf("app parts %v", keys)
	}
	if app.Bytes != keys[proto.UsageKeyBundle]+keys[proto.UsageKeyCache]+keys[proto.UsageKeyData] {
		t.Errorf("app %d != its parts %v", app.Bytes, keys)
	}
	data := groupOf(t, r.Node, proto.UsageKeyAppData)
	caches := findPath(data, filepath.Join(home, "Library", "Caches"))
	if caches == nil {
		t.Fatalf("no Caches in appdata %+v", data.Children)
	}
	if findPath(*caches, filepath.Join(home, "Library", "Caches", "com.example.app")) != nil {
		t.Error("the app's cache is counted twice: still under Caches")
	}
	gb := findPath(*caches, filepath.Join(home, "Library", "Caches", "go-build"))
	if gb == nil || gb.CacheID != "go-build" || !gb.CanTrash || gb.Category != proto.UsageCategoryCache {
		t.Errorf("go-build %+v", gb)
	}
	if findPath(data, filepath.Join(home, ".config")) == nil {
		t.Error("dot folders are not in appdata")
	}
	homeG := groupOf(t, r.Node, proto.UsageKeyHome)
	if findPath(homeG, filepath.Join(home, "Movies")) == nil || findPath(homeG, filepath.Join(home, "Library")) != nil {
		t.Errorf("home %+v", homeG.Children)
	}
	sysG := groupOf(t, r.Node, proto.UsageKeySystem)
	if findPath(sysG, sys) == nil || findPath(sysG, filepath.Join(apps, "Utilities")) == nil {
		t.Errorf("system %+v", sysG.Children)
	}
	var unmeasured *proto.StorageUsageNode
	for i := range sysG.Children {
		if sysG.Children[i].Key == proto.UsageKeyUnmeasured {
			unmeasured = &sysG.Children[i]
		}
	}
	if unmeasured == nil || unmeasured.Bytes <= 0 {
		t.Errorf("no unmeasured box: %+v", sysG.Children)
	}
	// The second call is the cache's.
	if again := usage(t, m, ""); !again.Cached {
		t.Error("the overview was scanned again")
	}
}

func TestTrashUpdatesCacheInPlace(t *testing.T) {
	m, home, _, _ := fakeMac(t)
	usage(t, m, "")
	movies := filepath.Join(home, "Movies")
	trip := filepath.Join(movies, "trip.mov")
	before, ok := m.KnownBytes(trip)
	if !ok || before <= 0 {
		t.Fatalf("trip.mov not known: %d %v", before, ok)
	}
	homeBefore := m.cache.get(home).bytes
	m.Removed(trip, before)
	if f := m.cache.get(movies); f == nil || len(f.children) != 0 {
		t.Errorf("Movies still lists it: %+v", f)
	}
	if got := m.cache.get(home).bytes; got != homeBefore-before {
		t.Errorf("home %d, want %d", got, homeBefore-before)
	}
	r := usage(t, m, "")
	if !r.Cached || findPath(groupOf(t, r.Node, proto.UsageKeyHome), trip) != nil {
		t.Errorf("the overview still shows it")
	}
}

func TestCacheExpiresAndEvicts(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	c := newCache(2, time.Minute, func() time.Time { return now })
	c.put(&folder{path: "/a"})
	c.put(&folder{path: "/b"})
	c.get("/a")
	c.put(&folder{path: "/c"}) // /b is the least recently used
	if c.get("/b") != nil || c.get("/a") == nil || c.get("/c") == nil {
		t.Error("LRU order")
	}
	now = now.Add(2 * time.Minute)
	if c.get("/a") != nil {
		t.Error("an entry outlived its time")
	}
	c.put(&folder{path: "/x"})
	c.put(&folder{path: "/x/y"})
	c.drop("/x")
	if c.len() != 0 {
		t.Errorf("drop left %d", c.len())
	}
}

func TestCheck(t *testing.T) {
	m, home, _, _ := fakeMac(t)
	movie := filepath.Join(home, "Movies", "trip.mov")
	items := m.Check([]string{movie, filepath.Join(home, "Library", "Preferences"), filepath.Join(home, "nope"), "relative"},
		proto.StoragePurposeTrash)
	if !items[0].OK || items[0].Kind != proto.UsageKindFile {
		t.Errorf("movie %+v", items[0])
	}
	if items[1].OK || items[1].Refusal != proto.RefusalAppManaged || items[1].Kind != proto.UsageKindDir {
		t.Errorf("Preferences %+v", items[1])
	}
	if items[2].OK || items[2].Refusal != proto.RefusalNotFound || items[3].Refusal != proto.RefusalNotFound {
		t.Errorf("missing %+v %+v", items[2], items[3])
	}
	link := filepath.Join(home, "Movies", "link.mov")
	if err := os.Symlink(movie, link); err != nil {
		t.Fatal(err)
	}
	if it := m.Check([]string{link}, proto.StoragePurposeOffload)[0]; it.OK || it.Refusal != proto.RefusalSymlink {
		t.Errorf("link %+v", it)
	}
	_ = context.Background()
}
