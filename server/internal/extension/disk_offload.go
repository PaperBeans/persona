package extension

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/nwverse/persona/sdk/go/wire"
)

// Disk offload providers (docs/server/machine-usage-map.md "Extension point disk_offload"): an extension's
// disk_offload contribution moves items from a computer to storage elsewhere, lists what it holds of each computer
// and brings items back. The registry only says which contributions are installed and on; asking a program
// (offload.inventory, offload.run, offload.job, offload.cancel) is the manager's (extmgr/extension_offload.go), after
// the user confirmed and the node's protection rules agreed to every path. Core never names a provider.

// DiskOffloadItem is a contribution to disk_offload.
type DiskOffloadItem = wire.DiskOffloadItem

// The bounds of one disk_offload item, the same as manifest.schema.json's DiskOffloadItem.
const diskOffloadNameMost = 40

// DiskOffloadDefaultKinds are what a provider takes when its item names no kinds.
var DiskOffloadDefaultKinds = []string{wire.DiskOffloadKindFile, wire.DiskOffloadKindDir}

// checkDiskOffload holds one disk_offload item to the point's rules (Points' Item).
func checkDiskOffload(it DiskOffloadItem) error {
	if !idPattern.MatchString(it.ID) {
		return fmt.Errorf("disk_offload id %q: a-z0-9_-", it.ID)
	}
	if !shortText(it.Name, diskOffloadNameMost) {
		return fmt.Errorf("disk_offload %s: a name of 1 to %d characters, no control characters", it.ID, diskOffloadNameMost)
	}
	if it.Kinds != nil && len(it.Kinds) == 0 {
		return fmt.Errorf("disk_offload %s: kinds is left out or names at least one", it.ID)
	}
	for i, k := range it.Kinds {
		if !slices.Contains(wire.DiskOffloadKinds, k) {
			return fmt.Errorf("disk_offload %s: kind %q: one of %s", it.ID, k, strings.Join(wire.DiskOffloadKinds, ", "))
		}
		if slices.Contains(it.Kinds[:i], k) {
			return fmt.Errorf("disk_offload %s: kind %q twice", it.ID, k)
		}
	}
	return nil
}

// DiskOffloads are the manifest's disk_offload contributions that hold to the point's rules.
func (m *Manifest) DiskOffloads() []DiskOffloadItem {
	var items, out []DiskOffloadItem
	_ = json.Unmarshal(m.Contributes["disk_offload"], &items)
	for _, it := range items {
		if checkDiskOffload(it) == nil {
			out = append(out, it)
		}
	}
	return out
}

// OffloadKinds is what it takes: its kinds, or files and folders.
func OffloadKinds(it DiskOffloadItem) []string {
	if len(it.Kinds) == 0 {
		return slices.Clone(DiskOffloadDefaultKinds)
	}
	return slices.Clone(it.Kinds)
}

// DiskOffloadProvider is one installed disk_offload contribution, on or off.
type DiskOffloadProvider struct {
	// Extension is the extension's canonical id (after a rename), Contribution the item's id.
	Extension, Contribution string
	// Name is what the map calls it ("Offload to …").
	Name string
	// Kinds are what it takes (OffloadKinds); Restore says it brings items back.
	Kinds   []string
	Restore bool
	// On: the extension is enabled, has no problem and the contribution is switched on. Whether its program runs is
	// the manager's to say.
	On bool
}

// diskOffloadsOf are p's disk_offload contributions as installed (Full), each On when it takes effect. Called with
// r.mu held; id is p's canonical id.
func diskOffloadsOf(id string, p *Extension) []DiskOffloadProvider {
	if p == nil || p.Full == nil {
		return nil
	}
	var on []string
	if p.Record.Enabled && p.Problem == "" && p.Manifest != nil {
		on = p.Manifest.Items("disk_offload")
	}
	var out []DiskOffloadProvider
	for _, it := range p.Full.DiskOffloads() {
		out = append(out, DiskOffloadProvider{Extension: id, Contribution: it.ID, Name: it.Name, Kinds: OffloadKinds(it),
			Restore: it.Restore, On: slices.Contains(on, it.ID)})
	}
	return out
}

// DiskOffloadProviders are every installed disk_offload contribution, on or off, by extension id and then in manifest
// order.
func (r *Registry) DiskOffloadProviders() []DiskOffloadProvider {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []DiskOffloadProvider
	for _, id := range r.sortedIDs() {
		out = append(out, diskOffloadsOf(id, r.extensions[id])...)
	}
	return out
}

// DiskOffloadProviderOf is the contribution of extension (followed through renames), on or off.
func (r *Registry) DiskOffloadProviderOf(extension, contribution string) (DiskOffloadProvider, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := r.canonicalID(extension)
	for _, p := range diskOffloadsOf(id, r.extensions[id]) {
		if p.Contribution == contribution {
			return p, true
		}
	}
	return DiskOffloadProvider{}, false
}
