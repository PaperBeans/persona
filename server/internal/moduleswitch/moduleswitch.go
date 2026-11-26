// Package moduleswitch is the run-time module switch as the core's provider seams read it (docs/architecture/
// core-and-modules.md, "The switch"): the ids the `modules` settings row switched off when a manager started, and the
// one predicate every seam asks, Set.On.
//
// A module that reaches the core through a provider seam (a port the composition root fills in its init, such as
// extmgr/githubport or builtinext) is switched off by that port: the port keeps its provider under the module's id and
// hands it out only to a caller whose Set has the module on. A switched-off module is then absent exactly as one left
// out of the build. The Set is a value, never process state, because one process can run several managers
// (internal/manager); each passes its own.
//
// Every port also records its seam here (Provided), so one test over the composition roots (internal/modules/all and
// internal/modules/all/node) checks that each seam a module fills follows the switch.
package moduleswitch

import (
	"slices"
	"sort"
	"strings"
	"sync"
)

// Set is the switch a manager (or a node, from its hub's features) started with. The zero value has every module on.
type Set struct {
	off []string // sorted, no duplicates
}

// Off is the Set with the given module ids switched off; empty ids are ignored.
func Off(ids ...string) Set {
	var off []string
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			off = append(off, id)
		}
	}
	sort.Strings(off)
	return Set{off: slices.Compact(off)}
}

// On reports whether the module id is switched on: the one predicate the provider seams ask.
func (s Set) On(id string) bool {
	_, found := slices.BinarySearch(s.off, id)
	return !found
}

// IDs is the sorted list of the ids switched off.
func (s Set) IDs() []string { return slices.Clone(s.off) }

// The flags a hub announces (the kernel's FeatureMarker and FeatureFlag; repeated here so a node needs no assistant).
const (
	// Marker is the feature a manager that publishes module flags always announces.
	Marker = "modules"
	// FlagPrefix starts every module's feature flag: module.<id>.
	FlagPrefix = "module."
)

// FromFeatures is the switch a node reads from its hub's features (node.configure's hub_features), by the clients'
// rule (#13844): with the Marker present, a module of known whose flag module.<id> is missing is off; without the
// Marker (an older hub, no manager connected yet) every module is on.
func FromFeatures(features []string, known []string) Set {
	if !slices.Contains(features, Marker) {
		return Set{}
	}
	var off []string
	for _, id := range known {
		if !slices.Contains(features, FlagPrefix+id) {
			off = append(off, id)
		}
	}
	return Off(off...)
}

// Seam is one provider seam a module filled: the port's name and the module's id, and Present, which answers
// whether the port hands the module's provider to a caller with the given Set.
type Seam struct {
	Port    string
	Module  string
	Present func(Set) bool
}

var (
	mu    sync.Mutex
	seams []Seam
)

// Provided records that the module filled the port; ports call it from their Provide.
func Provided(port, module string, present func(Set) bool) {
	mu.Lock()
	defer mu.Unlock()
	seams = append(seams, Seam{Port: port, Module: module, Present: present})
}

// Seams lists the seams provided so far (in a binary, every one the composition roots linked).
func Seams() []Seam {
	mu.Lock()
	defer mu.Unlock()
	return slices.Clone(seams)
}

// Modules lists the module ids that provided a seam to a port.
func Modules(port string) []string {
	var ids []string
	for _, s := range Seams() {
		if s.Port == port && !slices.Contains(ids, s.Module) {
			ids = append(ids, s.Module)
		}
	}
	return ids
}
