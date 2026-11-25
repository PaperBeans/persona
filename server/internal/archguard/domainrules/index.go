package domainrules

import "strings"

// computing is the standard library a core may import: what computes from its arguments alone.
var computing = map[string]bool{
	"bytes": true, "cmp": true, "iter": true, "maps": true, "math": true, "math/bits": true, "slices": true,
	"sort": true, "strconv": true, "strings": true, "unicode": true, "unicode/utf8": true,
}

// kitForCores is the kit a core may import, by directory under the root; clock is the application's.
var kitForCores = map[string]bool{
	"internal/domainkit/outcome": true, "internal/domainkit/fail": true, "internal/domainkit/instant": true,
	"internal/domainkit/opt": true, "internal/domainkit/defect": true, "internal/domainkit/slot": true,
}

// noCore are the parts that import no core, kit or adapter (path-no-core): the hub relays contracts, and db and
// prompts are libraries the hub links.
var noCore = []string{"internal/hub", "internal/hubedge", "internal/db", "internal/prompts", "cmd/persona-hub"}

// managerTrees are the trees a shared kernel stays out of: it lives directly under internal, beside them.
var managerTrees = map[string]bool{
	"internal/assistant": true, "internal/devices": true, "internal/extmgr": true, "internal/manager": true,
	"internal/node": true, "internal/hub": true,
}

// An Index is every package Load read, by import path, with the lists the rules read.
type Index struct {
	module    string
	pkgs      map[string]*Pkg
	contracts map[string]bool // import paths testdata/contracts.txt lists
	rules     string          // domainrules' own import path
	tree      *tree           // where the packages were read
}

// NewIndex indexes pkgs; contracts is testdata/contracts.txt's entries (paths under the module, or full import
// paths for another module's).
func NewIndex(module string, pkgs []*Pkg, contracts []string) Index {
	idx := Index{module: module, pkgs: map[string]*Pkg{}, contracts: map[string]bool{},
		rules: module + "/internal/archguard/domainrules"}
	for _, p := range pkgs {
		idx.pkgs[p.Path] = p
		idx.tree = p.tree
	}
	for _, c := range contracts {
		if strings.Contains(strings.SplitN(c, "/", 2)[0], ".") {
			idx.contracts[c] = true
		} else {
			idx.contracts[module+"/"+c] = true
		}
	}
	return idx
}

// layer is the layer of the package at an import path: the one it declares; contract for an undeclared package
// contracts.txt lists; undeclared for another package of the tree; "" for one outside it (standard library,
// third-party, another module's unlisted package).
func (idx Index) layer(path string) string {
	p := idx.pkgs[path]
	switch {
	case p != nil && p.Layer != "":
		return p.Layer
	case idx.contracts[path]:
		return layerContract
	case p != nil:
		return undeclared
	}
	return ""
}

// rel is an import path as messages name it: under the root for the tree's packages, whole otherwise.
func (idx Index) rel(path string) string {
	if r, ok := strings.CutPrefix(path, idx.module+"/"); ok {
		return r
	}
	return path
}

// core says whether a layer is a core's (a shared kernel's included).
func core(layer string) bool { return layer == layerCore || layer == layerShared }

// domain says whether the package at path is a core, shared kernel or kit package: what out-* rules guard.
func (idx Index) domain(path string) bool {
	l := idx.layer(path)
	return core(l) || l == layerKit
}

// typeNamed is the declaration of the type name in the package at path (tree.typeNamed), and whether the rules can
// read that package: false for a package outside the tree, such as another module's contract.
func (idx Index) typeNamed(path, name string) (*typeDecl, bool, error) {
	dir, ok := strings.CutPrefix(path, idx.module+"/")
	if path == idx.module {
		dir, ok = "", true
	}
	if !ok || idx.tree == nil {
		return nil, false, nil
	}
	td, err := idx.tree.typeNamed(dir, name)
	return td, true, err
}

// pkg is the package at path, or nil.
func (idx Index) pkg(path string) *Pkg { return idx.pkgs[path] }

// local is the name a file refers to an import by: as written, else the imported package's name (the last path
// element for a package outside the tree).
func (idx Index) local(imp Import) string {
	if imp.Name != "" {
		return imp.Name
	}
	if p := idx.pkgs[imp.Path]; p != nil && p.Name != "" {
		return p.Name
	}
	return importName(imp)
}

// imports is a file's imports by the name it uses for each ("." and "_" left out).
func (idx Index) imports(f *File) map[string]Import {
	out := map[string]Import{}
	for _, imp := range f.Imports {
		if n := idx.local(imp); n != "." && n != "_" {
			out[n] = imp
		}
	}
	return out
}

// kitName is the name f imports a kit package under ("outcome" for internal/domainkit/outcome), "" when it does not.
func (idx Index) kitName(f *File, kit string) string {
	for _, imp := range f.Imports {
		if imp.Path == idx.module+"/internal/domainkit/"+kit {
			return idx.local(imp)
		}
	}
	return ""
}

// stdName is the name f imports a standard library package under, "" when it does not.
func (idx Index) stdName(f *File, path string) string {
	for _, imp := range f.Imports {
		if imp.Path == path {
			return idx.local(imp)
		}
	}
	return ""
}

// under says whether rel is dir or below it.
func under(rel, dir string) bool { return rel == dir || strings.HasPrefix(rel, dir+"/") }
