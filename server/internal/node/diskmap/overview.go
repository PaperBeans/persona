package diskmap

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// The overview (docs/server/machine-usage-map.md "machine.storage.usage"): the volume that holds the home folder, in
// fixed groups whose bytes add up to it. One scan walks every root (home, the app folders, the system folders on the
// same volume); the groups are put together from what it measured, so a trash that updates the cached folders
// updates the overview with them.

// errNoHome: the node has no home folder to describe.
var errNoHome = errors.New("no home folder")

// overviewPlan is what the overview walks on this computer.
type overviewPlan struct {
	roots    []string        // every folder walked, home first
	deep     map[string]bool // folders whose every child is kept, one level deeper than the prefetch
	appRoots []string        // folders whose .app bundles are apps (/Applications; ~/Applications is in home)
	system   []string        // folders outside home measured as system
}

func (m *Map) plan() overviewPlan {
	home := m.opt.Home
	p := overviewPlan{roots: []string{home}, deep: map[string]bool{home: true}}
	lib := filepath.Join(home, "Library")
	appRoots, system := m.opt.AppRoots, m.opt.SystemRoots
	switch m.opt.GOOS {
	case "darwin":
		for _, d := range []string{"Caches", "Application Support", "Containers"} {
			p.deep[filepath.Join(lib, d)] = true
		}
		p.deep[lib] = true
		p.deep[filepath.Join(home, "Applications")] = true
		if appRoots == nil {
			appRoots = []string{"/Applications"}
		}
		if system == nil {
			system = []string{"/Library", "/opt", "/usr/local", "/private/var"}
		}
	case "linux":
		if system == nil {
			system = []string{"/opt", "/usr/local", "/var"}
		}
	case "windows":
		p.deep[filepath.Join(home, "AppData")] = true
		if system == nil {
			for _, v := range []string{"ProgramFiles", "ProgramFiles(x86)", "ProgramData"} {
				if d := m.opt.Getenv(v); d != "" && !slices.Contains(system, d) {
					system = append(system, d)
				}
			}
		}
	}
	real := func(r string) string {
		if x, err := filepath.EvalSymlinks(r); err == nil {
			return x
		}
		return r
	}
	for _, r := range appRoots {
		r = real(r)
		if sameVolume(m.opt.GOOS, home, r) {
			p.appRoots = append(p.appRoots, r)
			p.roots = append(p.roots, r)
			p.deep[r] = true
		}
	}
	for _, r := range system {
		r = real(r)
		if sameVolume(m.opt.GOOS, home, r) && !slices.Contains(p.roots, r) {
			p.system = append(p.system, r)
			p.roots = append(p.roots, r)
		}
	}
	return p
}

// sameVolume says whether dir is a folder on home's filesystem (another one is not this volume's to count).
func sameVolume(goos, home, dir string) bool {
	if goos == "windows" {
		return strings.EqualFold(filepath.VolumeName(home), filepath.VolumeName(dir)) && dirExists(dir)
	}
	hi, err1 := os.Stat(home)
	di, err2 := os.Lstat(dir)
	if err1 != nil || err2 != nil || !di.IsDir() {
		return false
	}
	hs, ok1 := statOf(hi)
	ds, ok2 := statOf(di)
	return ok1 && ok2 && hs.dev == ds.dev
}

func dirExists(dir string) bool {
	info, err := os.Lstat(dir)
	return err == nil && info.IsDir()
}

// overview answers the overview: from the cache when every root is there, else from the scan it starts or joins.
func (m *Map) overview(ctx context.Context, rescan bool, wait time.Duration, out proto.StorageUsageReport) (proto.StorageUsageReport, error) {
	if m.opt.Home == "" {
		return out, errNoHome
	}
	p := m.plan()
	if rescan && !m.running("") {
		m.cache.drop("")
	}
	b := m.builder(ctx, nil)
	cached := !m.running("")
	for _, r := range p.roots {
		if f := m.cache.get(r); f == nil || !f.listed {
			cached = false
		}
	}
	if cached {
		out.Cached = true
	} else {
		j := m.start("", p.roots, p.deep, m.opt.OverviewDeadline)
		waitFor(ctx, j, wait)
		b.views = j.views()
		if !j.finished() {
			out.State = proto.StorageUsageScanning
		}
	}
	vol := m.volume(m.opt.Home)
	out.Volume = vol
	out.Node = b.overviewNode(p, vol)
	for i, r := range p.roots {
		f := b.lookup(r)
		if f == nil {
			continue
		}
		if i == 0 || f.scannedAt.Before(time.Unix(out.ScannedAt, 0)) {
			out.ScannedAt = f.scannedAt.Unix()
		}
		out.DurationMS = max(out.DurationMS, f.duration.Milliseconds())
		out.Partial = out.Partial || f.partial && out.State == proto.StorageUsageReady
	}
	return out, nil
}

// overviewNode is the overview's tree: apps, home, appdata, system (with what is not measured) and free.
func (b *builder) overviewNode(p overviewPlan, vol proto.StorageUsageVolume) proto.StorageUsageNode {
	r := b.m.rules
	home := b.m.opt.Home
	homeF := b.lookup(home)
	lib := filepath.Join(home, "Library")

	var appItems []item
	for _, root := range p.appRoots {
		if f := b.lookup(root); f != nil {
			for _, it := range f.children {
				if it.kind == kindDir && strings.EqualFold(filepath.Ext(it.name), ".app") {
					appItems = append(appItems, it)
				}
			}
		}
	}
	var homeKids, dataKids, sysKids []proto.StorageUsageNode
	if homeF != nil {
		for _, it := range homeF.children {
			switch {
			case it.kind == kindMount || it.bytes <= 0:
			case r.goos == "darwin" && it.name == "Applications":
				if f := b.lookup(it.path); f != nil {
					for _, a := range f.children {
						if a.kind == kindDir && strings.EqualFold(filepath.Ext(a.name), ".app") {
							appItems = append(appItems, a)
						}
					}
				}
			case r.goos == "darwin" && it.name == "Library", r.goos == "windows" && strings.EqualFold(it.name, "AppData"):
				// filled in below, after the apps claimed their parts of it
			case strings.HasPrefix(it.name, "."):
				dataKids = append(dataKids, b.withPrefetch(it))
			default:
				homeKids = append(homeKids, b.withPrefetch(it))
			}
		}
	}
	var appNodes []proto.StorageUsageNode
	if r.goos == "darwin" {
		appNodes = b.apps(appItems, lib)
	}
	if homeF != nil {
		for _, it := range homeF.children {
			if r.goos == "darwin" && it.name == "Library" || r.goos == "windows" && strings.EqualFold(it.name, "AppData") {
				if f := b.lookup(it.path); f != nil && f.listed {
					dataKids = append(dataKids, b.itemNodes(f, true)...)
				} else if it.bytes > 0 {
					dataKids = append(dataKids, b.withPrefetch(it))
				}
			}
		}
	}
	for _, root := range p.appRoots {
		if f := b.lookup(root); f != nil {
			for _, it := range f.children {
				if !(it.kind == kindDir && strings.EqualFold(filepath.Ext(it.name), ".app")) && it.bytes > 0 && it.kind != kindMount {
					sysKids = append(sysKids, b.withPrefetch(it))
				}
			}
		}
	}
	for _, root := range p.system {
		f := b.lookup(root)
		if f == nil || f.bytes <= 0 {
			continue
		}
		n := b.pathNode(item{name: root, path: root, kind: kindDir, bytes: f.bytes, usedAt: f.usedAt, partial: f.partial})
		n.Children = b.children(f, prefetchMost, n.ID, false)
		sysKids = append(sysKids, n)
	}

	var groups []proto.StorageUsageNode
	add := func(key, category string, kids []proto.StorageUsageNode) {
		if len(kids) == 0 {
			return
		}
		groups = append(groups, group(key, proto.UsageKindGroup, category, limitNodes(kids, listMost, "group:"+key, category, 0, 0, 0)))
	}
	add(proto.UsageKeyApps, proto.UsageCategoryApps, appNodes)
	add(proto.UsageKeyHome, proto.UsageCategoryPersonal, homeKids)
	add(proto.UsageKeyAppData, proto.UsageCategoryAppData, dataKids)
	var measured int64
	for _, g := range groups {
		measured += g.Bytes
	}
	sys := limitNodes(sysKids, listMost, "group:"+proto.UsageKeySystem, proto.UsageCategorySystem, 0, 0, 0)
	for _, n := range sys {
		measured += n.Bytes
	}
	if rest := vol.UsedBytes - measured; rest > 0 {
		sys = append(sys, proto.StorageUsageNode{ID: "group:" + proto.UsageKeyUnmeasured, Key: proto.UsageKeyUnmeasured,
			Kind: proto.UsageKindUnmeasured, Category: proto.UsageCategorySystem, Bytes: rest})
	}
	if len(sys) > 0 {
		groups = append(groups, group(proto.UsageKeySystem, proto.UsageKindGroup, proto.UsageCategorySystem, sys))
	}
	if vol.FreeBytes > 0 {
		groups = append(groups, proto.StorageUsageNode{ID: "group:" + proto.UsageKeyFree, Key: proto.UsageKeyFree, Kind: proto.UsageKindFree,
			Category: proto.UsageCategoryFree, Bytes: vol.FreeBytes})
	}
	root := group("root", proto.UsageKindRoot, proto.UsageCategoryOther, groups)
	root.Bytes = max(root.Bytes, vol.TotalBytes) // boxes beyond the volume (clones, snapshots): the root grows to fit
	root.HasChildren = true
	return root
}

// withPrefetch is it as a node, carrying what is known of its children.
func (b *builder) withPrefetch(it item) proto.StorageUsageNode {
	n := b.pathNode(it)
	if it.kind == kindDir {
		if sub := b.lookup(it.path); sub != nil {
			n.Children = b.children(sub, prefetchMost, n.ID, false)
		}
	}
	return n
}

// apps are the app nodes of bundles: each with its bundle, its cache (~/Library/Caches/<bundle id>) and its data
// (~/Library/Application Support/<bundle id or name>, ~/Library/Containers/<bundle id>). What an app claims is taken
// out of those folders (b.adjust), so nothing is counted twice.
func (b *builder) apps(bundles []item, lib string) []proto.StorageUsageNode {
	caches := b.lookup(filepath.Join(lib, "Caches"))
	support := b.lookup(filepath.Join(lib, "Application Support"))
	containers := b.lookup(filepath.Join(lib, "Containers"))
	claimed := map[string]bool{}
	claim := func(f *folder, names ...string) (item, bool) {
		if f == nil {
			return item{}, false
		}
		for _, name := range names {
			if name == "" {
				continue
			}
			for _, it := range f.children {
				if strings.EqualFold(it.name, name) && !claimed[it.path] && it.bytes > 0 && it.kind == kindDir {
					claimed[it.path] = true
					a := b.adjust[f.path]
					a.minus += it.bytes
					if a.hide == nil {
						a.hide = map[string]bool{}
					}
					a.hide[it.path] = true
					b.adjust[f.path] = a
					return it, true
				}
			}
		}
		return item{}, false
	}
	var out []proto.StorageUsageNode
	seen := map[string]bool{}
	for _, bundle := range bundles {
		if seen[bundle.path] || bundle.bytes <= 0 {
			continue
		}
		seen[bundle.path] = true
		info := b.m.apps.get(bundle.path)
		name := strings.TrimSuffix(bundle.name, filepath.Ext(bundle.name))
		n := b.pathNode(bundle)
		n.Kind, n.Category, n.Name = proto.UsageKindApp, proto.UsageCategoryApps, name
		parts := []proto.StorageUsageNode{{ID: "group:bundle:" + bundle.path, Key: proto.UsageKeyBundle, Kind: proto.UsageKindGroup,
			Category: proto.UsageCategoryApps, Bytes: bundle.bytes, UsedAt: bundle.usedAt, Partial: bundle.partial}}
		if info.bundleID != "" {
			if c, ok := claim(caches, info.bundleID); ok {
				cn := b.pathNode(c)
				cn.Key = proto.UsageKeyCache
				parts = append(parts, cn)
			}
		}
		var data []proto.StorageUsageNode
		if d, ok := claim(support, info.bundleID, name); ok {
			data = append(data, b.pathNode(d))
		}
		if info.bundleID != "" {
			if d, ok := claim(containers, info.bundleID); ok {
				data = append(data, b.pathNode(d))
			}
		}
		switch len(data) {
		case 0:
		case 1:
			data[0].Key = proto.UsageKeyData
			parts = append(parts, data[0])
		default:
			dg := group("data:"+bundle.path, proto.UsageKindGroup, proto.UsageCategoryAppData, data)
			dg.Key = proto.UsageKeyData
			parts = append(parts, dg)
		}
		n.Bytes, n.UsedAt = 0, 0
		for _, part := range parts {
			n.Bytes += part.Bytes
			n.UsedAt = max(n.UsedAt, part.UsedAt)
			n.Partial = n.Partial || part.Partial
		}
		n.Children, n.HasChildren = parts, true
		out = append(out, n)
	}
	return out
}
