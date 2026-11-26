package diskmap

import (
	"cmp"
	"context"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/proto"
)

// builder turns measured folders into the map's nodes, with the rules' flags on every box that is one path.
type builder struct {
	m     *Map
	ctx   context.Context
	views map[string]*folder // a running scan's folders, read before the cache
	inUse map[string]bool
	// adjust are the overview's corrections: bytes an app claimed out of a folder, and the children it took.
	adjust map[string]adjustment
}

type adjustment struct {
	minus int64
	hide  map[string]bool // children's paths
}

func (m *Map) builder(ctx context.Context, views map[string]*folder) *builder {
	return &builder{m: m, ctx: ctx, views: views, adjust: map[string]adjustment{}}
}

// lookup is what is known of the folder at path: the running scan's, else the cache's.
func (b *builder) lookup(path string) *folder {
	if f := b.views[path]; f != nil {
		return f
	}
	return b.m.cache.get(path)
}

// folderNode is f as the root of a folder report: its children, each with its own largest children when known.
func (b *builder) folderNode(f *folder) proto.StorageUsageNode {
	n := b.pathNode(item{name: filepath.Base(f.path), path: f.path, kind: kindDir, bytes: f.bytes, usedAt: f.usedAt, partial: f.partial})
	n.Children = b.children(f, listMost, n.ID, true)
	return n
}

// children are f's children as nodes, the most largest of them and one "other" for the rest; with prefetch, each
// folder among them carries what is known of its own children.
func (b *builder) children(f *folder, most int, parentID string, prefetch bool) []proto.StorageUsageNode {
	nodes := b.itemNodes(f, prefetch)
	return limitNodes(nodes, most, parentID, b.m.rules.category(f.path), f.otherBytes, f.otherItems, f.otherUsedAt)
}

// itemNodes are f's children as nodes, largest first, with the overview's corrections applied.
func (b *builder) itemNodes(f *folder, prefetch bool) []proto.StorageUsageNode {
	adj := b.adjust[f.path]
	var out []proto.StorageUsageNode
	for _, it := range f.children {
		if adj.hide[it.path] {
			continue
		}
		a, adjusted := b.adjust[it.path]
		if adjusted {
			it.bytes = max(it.bytes-a.minus, 0)
		}
		if it.bytes <= 0 && (adjusted || it.kind != kindFile) {
			continue
		}
		n := b.pathNode(it)
		if prefetch && it.kind == kindDir {
			if sub := b.lookup(it.path); sub != nil {
				n.Children = b.children(sub, prefetchMost, n.ID, false)
			}
		}
		out = append(out, n)
	}
	return out
}

// limitNodes sorts nodes largest first and keeps most of them; the rest, and what was already summed (otherBytes of
// otherItems), become one "other" box.
func limitNodes(nodes []proto.StorageUsageNode, most int, parentID, category string, otherBytes int64, otherItems int, otherUsed int64) []proto.StorageUsageNode {
	slices.SortStableFunc(nodes, func(a, b proto.StorageUsageNode) int {
		return cmp.Or(cmp.Compare(b.Bytes, a.Bytes), cmp.Compare(a.Name, b.Name))
	})
	if len(nodes) > most {
		for _, n := range nodes[most:] {
			otherBytes += n.Bytes
			otherItems++
			otherUsed = max(otherUsed, n.UsedAt)
		}
		nodes = nodes[:most]
	}
	if otherBytes > 0 {
		nodes = append(nodes, proto.StorageUsageNode{ID: "group:other:" + parentID, Key: proto.UsageKeyOther, Kind: proto.UsageKindOther,
			Category: category, Bytes: otherBytes, Items: otherItems, UsedAt: otherUsed})
	}
	return nodes
}

// pathNode is one box that is exactly one path, with the rules' answer for it.
func (b *builder) pathNode(it item) proto.StorageUsageNode {
	r := b.m.rules
	n := proto.StorageUsageNode{ID: it.path, Name: it.name, Path: it.path, Display: r.display(it.path), Kind: proto.UsageKindDir,
		Category: r.category(it.path), Bytes: it.bytes, UsedAt: it.usedAt, Partial: it.partial, HasChildren: true}
	f := facts{exists: true, dir: it.kind != kindFile, regular: it.kind == kindFile, mount: it.kind == kindMount}
	switch it.kind {
	case kindFile:
		n.Kind, n.HasChildren = proto.UsageKindFile, false
	case kindMount:
		n.Kind = proto.UsageKindMount
	}
	if bundle, _ := r.appBundle(r.norm(it.path)); bundle && it.kind == kindDir {
		n.Kind = proto.UsageKindApp
		n.Name = strings.TrimSuffix(it.name, filepath.Ext(it.name))
		if info := b.m.apps.get(it.path); info.bundleID != "" {
			n.App = &proto.StorageUsageApp{BundleID: info.bundleID, Version: info.version}
		}
	}
	b.flag(&n, f)
	return n
}

// flag sets the rules' answer on n, and the catalog cache it is.
func (b *builder) flag(n *proto.StorageUsageNode, f facts) {
	r := b.m.rules
	n.Refusal = r.refusal(n.Path, proto.StoragePurposeTrash, f)
	n.CanTrash = n.Refusal == ""
	n.CanOffload = r.refusal(n.Path, proto.StoragePurposeOffload, f) == ""
	if id := r.cacheAt(r.norm(n.Path)); id != "" {
		n.CacheID = id
		n.Category = proto.UsageCategoryCache
		if b.inUse == nil {
			b.inUse = b.m.cachesInUse(b.ctx)
		}
		n.CacheInUse = b.inUse == nil || b.inUse[id]
	}
}

// group is a fixed box that is no one path: the sum of its children.
func group(key, kind, category string, children []proto.StorageUsageNode) proto.StorageUsageNode {
	n := proto.StorageUsageNode{ID: "group:" + key, Key: key, Kind: kind, Category: category, Children: children,
		HasChildren: len(children) > 0}
	for _, c := range children {
		n.Bytes += c.Bytes
		n.UsedAt = max(n.UsedAt, c.UsedAt)
		n.Partial = n.Partial || c.Partial
	}
	return n
}
