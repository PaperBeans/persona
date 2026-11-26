package domainrules

import (
	"go/ast"
	"strings"
)

// Layers is the rules on declarations and imports across packages: every package declares one known layer in its
// package comment (layer-declared, layer-one), and each declaration is a checked fact (layer-kit, layer-contract,
// layer-shared, layer-application, layer-library, layer-testkit: testkit is imported only by tests and other testkit
// and is no program, so its code reaches no binary); cores are imported only from the layers that may
// (core-importers), and domainrules only from tests (out-rules-import). Declared application packages, and shared
// kernels, must be parsed in full (Full) for the facts that read their code.
func Layers(pkgs []*Pkg, idx Index) []Finding {
	var fs findings
	for _, p := range pkgs {
		declaration(&fs, p, idx)
		for _, f := range p.Files {
			imports(&fs, p, f, idx)
		}
	}
	return fs.list
}

// declaration is the rules on one package's declaration.
func declaration(fs *findings, p *Pkg, idx Index) {
	name := display(p)
	var placed []directive
	for _, d := range p.declared {
		switch {
		case !d.placed:
			fs.add(d.pos, "layer-declared", p.Rel, name+" declares no layer: its //persona:layer line is outside its package comment")
		case d.layer == "":
			fs.add(d.pos, "layer-declared", p.Rel, name+" declares no layer: its //persona:layer line names none")
		case !knownLayer(d.layer):
			fs.add(d.pos, "layer-declared", p.Rel, name+" declares no layer: "+d.layer+" is no layer")
		default:
			placed = append(placed, d)
		}
	}
	if len(placed) == 0 {
		if len(p.declared) == 0 {
			fs.edge(first(p), "layer-declared", p.Rel, name, name+" declares no layer")
		}
		return
	}
	for _, d := range placed[1:] {
		if d.layer != placed[0].layer {
			fs.add(d.pos, "layer-one", p.Rel, name+" declares two layers")
			break
		}
	}
	listed := idx.contracts[p.Path]
	switch {
	case p.Layer == layerKit && !under(p.Rel, "internal/domainkit"):
		fs.add(placed[0].pos, "layer-kit", p.Rel, name+" declares kit outside internal/domainkit")
	case p.Layer == layerTestkit && p.Name == "main":
		// A program is imported by nothing, so who imports it cannot keep it out of production: it is application.
		fs.add(placed[0].pos, "layer-testkit", p.Rel, name+" declares testkit but is a program (package main)")
	case p.Layer == layerContract && !listed:
		fs.add(placed[0].pos, "layer-contract", p.Rel, name+" declares contract but is not in contracts.txt")
	case p.Layer != layerContract && listed:
		fs.add(placed[0].pos, "layer-contract", p.Rel, name+" is in contracts.txt but declares "+p.Layer)
	case p.Layer == layerShared:
		shared(fs, p, placed[0])
	case p.Layer == layerApp || p.Layer == layerSeam:
		if !effectful(p, idx) {
			fs.add(placed[0].pos, "layer-application", p.Rel, name+" declares "+p.Layer+" but computes only from its arguments")
		}
	}
}

// shared is layer-shared: a shared kernel lives directly under internal, outside the managers', the node's and the
// hub's trees, and holds only values and closed sets.
func shared(fs *findings, p *Pkg, d directive) {
	name := display(p)
	parts := strings.Split(p.Rel, "/")
	if len(parts) != 2 || parts[0] != "internal" || managerTrees[p.Rel] {
		fs.add(d.pos, "layer-shared", p.Rel, name+" declares core shared but lives outside server/internal's top level")
	}
	if p.decls == nil {
		return
	}
	for _, t := range p.decls.order {
		if strings.HasSuffix(t.name, "Builder") {
			fs.add(t.file.pos(t.spec.Name), "layer-shared", p.Rel, name+" declares core shared but declares the builder "+t.name)
		}
		for _, m := range p.decls.methods[t.name] {
			switch m.decl.Name.Name {
			case "ID":
				fs.add(m.file.pos(m.decl.Name), "layer-shared", p.Rel, name+" declares core shared but "+t.name+" has an ID, so it is an entity")
			case "Decide":
				fs.add(m.file.pos(m.decl.Name), "layer-shared", p.Rel, name+" declares core shared but declares "+t.name+".Decide")
			}
		}
	}
	for _, fd := range p.decls.funcs {
		fn := fd.decl.Name.Name
		if !exported(fn) || wordPrefix(fn, "Parse") || wordPrefix(fn, "New") || wordPrefix(fn, "Restore") {
			continue
		}
		rs := params(fd.decl.Type.Results)
		closed := false
		if len(rs) == 1 {
			if pkg, base := baseType(rs[0]); pkg == "" && p.decls.types[base] != nil && strings.HasPrefix(fn, base) && len(fn) > len(base) {
				closed = true
			}
		}
		if !closed {
			fs.add(fd.file.pos(fd.decl.Name), "layer-shared", p.Rel, name+" declares core shared but "+fn+" is a domain service")
		}
	}
}

// effectPkgs are the packages a call into counts as an effect use; effectTypes those a field's or parameter's type
// from counts as one too.
var (
	effectPkgs = map[string]bool{
		"os": true, "os/exec": true, "os/signal": true, "syscall": true, "net": true, "net/http": true, "net/rpc": true,
		"net/smtp": true, "database/sql": true, "log": true, "log/slog": true, "crypto/rand": true, "math/rand": true,
		"math/rand/v2": true,
	}
	effectTypes = map[string]bool{"net/http": true, "database/sql": true, "net": true, "os": true, "os/exec": true, "sync": true}
)

// effectful says whether an application package's code shows an effect use: a call into an effectful package or into
// an application package (an undeclared one included), a clock call, a go statement, a channel or a select, or a
// field or parameter of an effectful type. A context.Context parameter or an adapter import alone is none.
func effectful(p *Pkg, idx Index) bool {
	for _, f := range p.Files {
		if f.Test || f.ast == nil {
			continue
		}
		calls, types := map[string]bool{}, map[string]bool{}
		timeName := ""
		for name, imp := range idx.imports(f) {
			if effectPkgs[imp.Path] {
				calls[name] = true
			}
			if effectTypes[imp.Path] {
				types[name] = true
			}
			if l := idx.layer(imp.Path); l == layerApp || l == layerSeam || l == undeclared {
				calls[name] = true
			}
			if imp.Path == "time" {
				timeName = name
			}
		}
		found := false
		ast.Inspect(f.ast, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.GoStmt, *ast.ChanType, *ast.SelectStmt, *ast.SendStmt:
				found = true
			case *ast.CallExpr:
				if sel, ok := x.Fun.(*ast.SelectorExpr); ok {
					if id, ok := sel.X.(*ast.Ident); ok && (calls[id.Name] || id.Name == timeName && clockFuncs[sel.Sel.Name]) {
						found = true
					}
				}
			case *ast.Field:
				if pkg, _ := baseType(unwrapParam(x.Type)); types[pkg] {
					found = true
				}
			}
			return !found
		})
		if found {
			return true
		}
	}
	return false
}

// imports is the rules on one file's imports: core-importers, layer-library, layer-testkit and out-rules-import.
func imports(fs *findings, p *Pkg, f *File, idx Index) {
	layer := idx.layer(p.Path)
	for _, imp := range f.Imports {
		if imp.Path == idx.rules && !f.Test {
			fs.add(imp.Pos, "out-rules-import", p.Rel, f.Rel+" imports domainrules")
			continue
		}
		dep := idx.pkg(imp.Path)
		if dep == nil || dep == p && !f.External {
			continue
		}
		l := idx.layer(imp.Path)
		rel := idx.rel(imp.Path)
		switch {
		case l == layerTestkit && !f.Test && layer != layerTestkit:
			fs.add(imp.Pos, "layer-testkit", rel, f.Rel+" imports testkit "+rel+" from non-test code")
		}
		if f.Test {
			continue
		}
		domain := core(l) || l == layerKit || l == layerAdapter
		if layer == layerLibrary && domain {
			fs.add(imp.Pos, "layer-library", p.Rel, display(p)+" is a library but imports "+rel)
			continue
		}
		if !core(l) {
			continue
		}
		switch layer {
		case layerApp, layerSeam, undeclared, layerAdapter, layerTestkit:
		case layerCore, layerShared: // core-import says whether a core may import it
		default:
			fs.add(imp.Pos, "core-importers", rel, display(p)+" ("+layer+") imports core "+rel)
		}
	}
}
