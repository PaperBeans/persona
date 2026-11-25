// Package diskmap is a computer's disk usage map (#14576, docs/server/machine-usage-map.md): the node method
// machine.storage.usage sizes the disk as a tree the apps draw as a treemap, and the protection rules here decide what
// the map may offer to move to the trash or hand to an offload provider (machine.storage.check, and again before each
// trash).
//
// It is the node's own Go code on macOS, Linux and Windows; nothing is executed. A walk stays on one filesystem,
// never follows a symbolic link or a Windows reparse point, counts a hard-linked file once, and is bounded by a
// deadline and an entry budget. A call starts (or joins) the scan of its folder and waits at most wait_ms; what is
// measured by then is the answer, and the scan goes on for the next call. Results stay in memory for a while.
package diskmap

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// Bounds (docs/server/machine-usage-map.md "Bounds").
const (
	folderDeadline   = 20 * time.Second
	overviewDeadline = 120 * time.Second
	entryBudget      = 3_000_000
	scansAtOnce      = 2
	cacheMost        = 512
	cacheTTL         = 15 * time.Minute
	listMost         = 64 // children a node lists before the rest become one "other"
	prefetchMost     = 16 // children a prefetched child lists
	inUseFor         = 10 * time.Second
)

// Options are what a Map reads of the computer; tests set each.
type Options struct {
	GOOS   string
	Home   string
	Getenv func(string) string
	// Caches are the cache catalog's folders on this computer, by id (machdiag.CacheFolders).
	Caches map[string]string
	// CachesInUse says which catalog caches' toolchains run now (nil: none is known to).
	CachesInUse func(ctx context.Context) map[string]bool
	// AgentDirs and PersonaDirs are folders the rules never offer, beyond the fixed names.
	AgentDirs, PersonaDirs []string
	// SystemRoots are the folders outside home the overview measures (nil: the system's own list).
	SystemRoots []string
	// AppRoots are the folders whose bundles the overview groups as apps (nil: the system's own list).
	AppRoots []string
	Now      func() time.Time
	// Walkers, FolderDeadline, OverviewDeadline and Budget override the bounds (0: the defaults).
	Walkers                          int
	FolderDeadline, OverviewDeadline time.Duration
	Budget                           int64
	// Volume reads a filesystem's numbers (nil: statfs / GetDiskFreeSpaceEx).
	Volume func(path string) (total, free, used int64, err error)
}

// Map is one computer's usage map: its scans, its cache and its rules.
type Map struct {
	opt   Options
	rules ruleEnv
	gate  chan struct{}
	cache *cache
	apps  appInfos

	mu   sync.Mutex
	jobs map[string]*job

	inUseMu sync.Mutex
	inUse   map[string]bool
	inUseAt time.Time
}

// New is a Map for the computer opt describes.
func New(opt Options) *Map {
	if opt.GOOS == "" {
		opt.GOOS = runtime.GOOS
	}
	if real, err := filepath.EvalSymlinks(opt.Home); opt.Home != "" && err == nil {
		opt.Home = real // the rules judge real paths
	}
	if opt.Getenv == nil {
		opt.Getenv = os.Getenv
	}
	if opt.Now == nil {
		opt.Now = time.Now
	}
	if opt.Walkers <= 0 {
		opt.Walkers = 4
		if runtime.NumCPU() >= 8 {
			opt.Walkers = 8
		}
	}
	if opt.FolderDeadline <= 0 {
		opt.FolderDeadline = folderDeadline
	}
	if opt.OverviewDeadline <= 0 {
		opt.OverviewDeadline = overviewDeadline
	}
	if opt.Budget <= 0 {
		opt.Budget = entryBudget
	}
	return &Map{opt: opt, rules: ruleEnv{goos: opt.GOOS, home: opt.Home, caches: opt.Caches, agents: opt.AgentDirs,
		persona: opt.PersonaDirs}, gate: make(chan struct{}, scansAtOnce), cache: newCache(cacheMost, cacheTTL, opt.Now),
		jobs: map[string]*job{}}
}

// ErrBadPath is a usage path the map does not open: relative, a pseudo filesystem, or not a folder.
var ErrBadPath = errors.New("not a folder the map opens")

// pseudo are the kernel's own filesystems, never walked.
var pseudo = []string{"/proc", "/sys", "/dev", "/run"}

// Resolve is p as the node acts on it: "~" and "~/…" expanded, absolute, clean, and the folder it is in resolved
// through links (the last component is not followed).
func (m *Map) Resolve(p string) (string, error) {
	p = strings.TrimSpace(p)
	switch {
	case p == "~":
		p = m.opt.Home
	case strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`):
		p = filepath.Join(m.opt.Home, p[2:])
	}
	if p == "" || !filepath.IsAbs(p) {
		return "", ErrBadPath
	}
	p = filepath.Clean(p)
	dir, base := filepath.Dir(p), filepath.Base(p)
	if dir == p {
		return p, nil
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	return filepath.Join(dir, base), nil
}

// folderPath is a usage request's folder: resolved through every link (a folder to read may be one), and refused
// when it is a pseudo filesystem or no folder.
func (m *Map) folderPath(p string) (string, error) {
	r, err := m.Resolve(p)
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(r); err == nil {
		r = real
	}
	if m.opt.GOOS != "windows" {
		for _, x := range pseudo {
			if under(r, x) {
				return "", ErrBadPath
			}
		}
	}
	info, err := os.Stat(r)
	if err != nil || !info.IsDir() {
		return "", ErrBadPath
	}
	return r, nil
}

// Usage is machine.storage.usage.
func (m *Map) Usage(ctx context.Context, p proto.StorageUsageParams) (proto.StorageUsageReport, error) {
	wait := time.Duration(p.WaitMS) * time.Millisecond
	switch {
	case p.WaitMS <= 0:
		wait = proto.StorageUsageWaitDefaultMS * time.Millisecond
	case p.WaitMS > proto.StorageUsageWaitMostMS:
		wait = proto.StorageUsageWaitMostMS * time.Millisecond
	}
	out := proto.StorageUsageReport{At: m.opt.Now().Unix(), OS: m.opt.GOOS, Home: m.opt.Home, State: proto.StorageUsageReady}
	if strings.TrimSpace(p.Path) == "" {
		return m.overview(ctx, p.Rescan, wait, out)
	}
	path, err := m.folderPath(p.Path)
	if err != nil {
		return out, err
	}
	out.Path = path
	if p.Rescan {
		m.cache.drop(path)
	}
	b := m.builder(ctx, nil)
	if f := m.cache.get(path); f != nil && f.listed && !m.running(path) {
		out.Cached = true
		return m.folderReport(b, f, out), nil
	}
	j := m.start(path, []string{path}, nil, m.opt.FolderDeadline)
	waitFor(ctx, j, wait)
	views := j.views()
	b.views = views
	f := views[path]
	if f == nil {
		f = &folder{path: path}
	}
	if !j.finished() {
		out.State = proto.StorageUsageScanning
		f.partial = false
	}
	return m.folderReport(b, f, out), nil
}

// folderReport fills out with folder f's tree and its volume.
func (m *Map) folderReport(b *builder, f *folder, out proto.StorageUsageReport) proto.StorageUsageReport {
	out.Partial = f.partial
	out.ScannedAt = f.scannedAt.Unix()
	out.DurationMS = f.duration.Milliseconds()
	out.Volume = m.volume(f.path)
	out.Node = b.folderNode(f)
	return out
}

// volume is the numbers of the filesystem holding path.
func (m *Map) volume(path string) proto.StorageUsageVolume {
	v := proto.StorageUsageVolume{Mount: volumeName(m.opt.GOOS, path)}
	var err error
	if m.opt.Volume != nil {
		v.TotalBytes, v.FreeBytes, v.UsedBytes, err = m.opt.Volume(path)
	} else {
		var x volume
		x, err = volumeAt(path)
		v.TotalBytes, v.FreeBytes, v.UsedBytes = x.total, x.free, x.used
	}
	if err != nil {
		return proto.StorageUsageVolume{Mount: v.Mount}
	}
	return v
}

// volumeName is the mount point a report names: the drive on Windows, the filesystem's root elsewhere (found by
// walking up while the device stays the same).
func volumeName(goos, path string) string {
	if goos == "windows" {
		if v := filepath.VolumeName(path); v != "" {
			return v + `\`
		}
		return path
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "/"
	}
	st, ok := statOf(info)
	if !ok {
		return "/"
	}
	at := path
	for {
		up := filepath.Dir(at)
		if up == at {
			return at
		}
		ui, err := os.Lstat(up)
		if err != nil {
			return at
		}
		us, ok := statOf(ui)
		if !ok || us.dev != st.dev {
			return at
		}
		at = up
	}
}

// running says whether a scan of key runs now.
func (m *Map) running(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.jobs[key]
	return j != nil && !j.finished()
}

// start joins the scan of key, or starts one over roots.
func (m *Map) start(key string, roots []string, deep map[string]bool, deadline time.Duration) *job {
	m.mu.Lock()
	defer m.mu.Unlock()
	if j := m.jobs[key]; j != nil {
		return j
	}
	j := newJob(key, roots, deep, deadline, m.opt.Budget, m.opt.Walkers)
	m.jobs[key] = j
	go func() {
		j.run(m.gate)
		m.finish(j)
	}()
	return j
}

// finish keeps what a scan measured and lets the next call start a new one.
func (m *Map) finish(j *job) {
	views := j.views()
	for _, r := range j.roots {
		f := views[r.path]
		if f == nil {
			continue
		}
		if old := m.cache.get(r.path); old != nil {
			m.cache.shift(r.path, f.bytes-old.bytes)
		} else if parent := m.cache.get(filepath.Dir(r.path)); parent != nil {
			for _, it := range parent.children {
				if it.path == r.path {
					m.cache.shift(r.path, f.bytes-it.bytes)
				}
			}
		}
	}
	for _, f := range views {
		m.cache.put(f)
	}
	m.mu.Lock()
	if m.jobs[j.key] == j {
		delete(m.jobs, j.key)
	}
	m.mu.Unlock()
}

// waitFor waits until j is done, wait has passed or ctx ends.
func waitFor(ctx context.Context, j *job, wait time.Duration) {
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-j.done:
	case <-t.C:
	case <-ctx.Done():
	}
}

// KnownBytes is what the map last measured of path, when it did.
func (m *Map) KnownBytes(path string) (int64, bool) {
	if f := m.cache.get(path); f != nil && f.listed && !f.partial {
		return f.bytes, true
	}
	if parent := m.cache.get(filepath.Dir(path)); parent != nil {
		for _, it := range parent.children {
			if it.path == path && !it.partial && it.kind != kindMount {
				return it.bytes, true
			}
		}
	}
	return 0, false
}

// Removed updates the map after path (bytes) went to the trash: it leaves its parent and its bytes leave every
// measured folder above it, so the map is right without a rescan.
func (m *Map) Removed(path string, bytes int64) { m.cache.removed(path, bytes) }

// Facts are lstat's facts of path, as the rules read them.
func Facts(path string) (exists, dir, regular, link, mount bool) {
	info, err := os.Lstat(path)
	if err != nil {
		return false, false, false, false, false
	}
	st, ok := statOf(info)
	if !ok {
		return true, info.IsDir(), info.Mode().IsRegular(), info.Mode()&os.ModeSymlink != 0, false
	}
	if st.dir && !st.link {
		if pi, err := os.Lstat(filepath.Dir(path)); err == nil {
			if ps, ok := statOf(pi); ok && ps.dev != st.dev {
				mount = true
			}
		}
		if runtime.GOOS == "windows" && filepath.Dir(path) == path {
			mount = true
		}
	}
	return true, st.dir, st.regular, st.link, mount
}

// Refusal is the protection rules' answer for path (resolved: Resolve) and purpose, read on the real file; and its
// kind (proto.UsageKind*: file, dir or app; "" when it is not there).
func (m *Map) Refusal(path, purpose string) (refusal, kind string) {
	exists, dir, regular, link, mount := Facts(path)
	f := facts{exists: exists, dir: dir, regular: regular, link: link, mount: mount}
	switch {
	case !exists:
	case dir && m.isBundle(path):
		kind = proto.UsageKindApp
	case dir:
		kind = proto.UsageKindDir
	default:
		kind = proto.UsageKindFile
	}
	return m.rules.refusal(path, purpose, f), kind
}

// Check is machine.storage.check: each path's answer for purpose, without a side effect.
func (m *Map) Check(paths []string, purpose string) []proto.StorageCheckItem {
	out := make([]proto.StorageCheckItem, 0, len(paths))
	for _, p := range paths {
		it := proto.StorageCheckItem{Path: p}
		real, err := m.Resolve(p)
		if err != nil {
			it.Refusal = proto.RefusalNotFound
			out = append(out, it)
			continue
		}
		it.Path = real
		it.Refusal, it.Kind = m.Refusal(real, purpose)
		it.OK = it.Refusal == ""
		if b, ok := m.KnownBytes(real); ok {
			it.Bytes = b
		}
		out = append(out, it)
	}
	return out
}

// Display is path as the apps show it ("~/…" inside home).
func (m *Map) Display(path string) string { return m.rules.display(path) }

// cachesInUse is which catalog caches' toolchains run, asked at most every inUseFor.
func (m *Map) cachesInUse(ctx context.Context) map[string]bool {
	if m.opt.CachesInUse == nil {
		return nil
	}
	m.inUseMu.Lock()
	defer m.inUseMu.Unlock()
	if m.inUse == nil || m.opt.Now().Sub(m.inUseAt) > inUseFor {
		m.inUse, m.inUseAt = m.opt.CachesInUse(ctx), m.opt.Now()
	}
	return m.inUse
}

// isBundle says whether path is a macOS app bundle.
func (m *Map) isBundle(path string) bool {
	bundle, _ := m.rules.appBundle(m.rules.norm(path))
	return bundle
}

// sizeDeadline bounds Size's walk of an item the map has not measured.
const sizeDeadline = 5 * time.Second

// Size is the bytes path holds on its filesystem: what the map knows, else a walk of it within a few seconds and at
// most within (then a lower bound, complete false; nothing is walked when within is spent).
func (m *Map) Size(path string, within time.Duration) (bytes int64, complete bool) {
	if b, ok := m.KnownBytes(path); ok {
		return b, true
	}
	info, err := os.Lstat(path)
	if err != nil {
		return 0, true
	}
	st, ok := statOf(info)
	if !ok || st.link {
		return 0, true
	}
	if !st.dir {
		return st.bytes, true
	}
	if within <= 0 {
		return 0, false
	}
	j := newJob(path, []string{path}, nil, min(sizeDeadline, within), m.opt.Budget, m.opt.Walkers)
	j.roots[0].track = false
	gate := make(chan struct{}, 1)
	j.run(gate)
	return j.roots[0].bytes.Load(), !j.roots[0].cut.Load()
}

// Restored updates the map after path (bytes) came back from the trash: its parent is measured again when next
// opened, and the folders above it count it again.
func (m *Map) Restored(path string, bytes int64) { m.cache.restored(path, bytes) }

// Free is what an ordinary user can still write on the home folder's volume, read now.
func (m *Map) Free() int64 { return m.volume(m.opt.Home).FreeBytes }
