package domainrules

import (
	"go/ast"
	"go/token"
)

// A Count is one shrink-only count of an application package: how many of a habit the domain model replaces its
// production code has, and the names that make it up.
type Count struct {
	Key   string // the package, under the root
	Rule  string // app-exported-record, app-named-primitive, app-lifecycle-write or app-bridge-call
	N     int
	Names []string // what was counted: type names, or file:line for writes and calls
	Pos   token.Position
}

// counted are the application habits the counts hold: exported-field records, named primitive types, direct writes
// of an item's done state, and the domain values application code unwraps through an adapter's Wire.
var counted = []string{"app-exported-record", "app-named-primitive", "app-lifecycle-write", "app-bridge-call"}

// Ratchets is the app-* counts of every undeclared, application or library package (contracts, db and prompts left
// out for records), and app-named-primitive of every adapter: it parses their production files in full. A library is
// counted as application code is, so declaring library is no way around them: technical code holds no domain
// concept, and a new library starts at zero. An adapter owns its Record and writes it, so only its named primitives
// are counted: declaring adapter is no way around that count either, and a new adapter starts at zero.
func Ratchets(pkgs []*Pkg, idx Index) ([]Count, error) {
	var out []Count
	for _, p := range pkgs {
		l := idx.layer(p.Path)
		adapter := l == layerAdapter
		if !adapter && l != undeclared && l != layerApp && l != layerSeam && l != layerLibrary {
			continue
		}
		records := !adapter && !under(p.Rel, "internal/db") && !under(p.Rel, "internal/prompts") && !idx.contracts[p.Path]
		cs := map[string]*Count{}
		for _, rule := range counted {
			cs[rule] = &Count{Key: p.Rel, Rule: rule, Pos: first(p)}
		}
		for _, f := range p.Files {
			if f.Test {
				continue
			}
			if err := f.parse(); err != nil {
				return nil, err
			}
			imports := idx.imports(f)
			ast.Inspect(f.ast, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.SelectorExpr:
					// An adapter's Wire, called or passed on, hands application code the raw shape of a domain value.
					if id, ok := x.X.(*ast.Ident); ok && x.Sel.Name == "Wire" && !adapter {
						if imp, ok := imports[id.Name]; ok && idx.layer(imp.Path) == layerAdapter {
							c := cs["app-bridge-call"]
							c.N++
							pos := f.pos(x)
							c.Names = append(c.Names, pos.Filename+":"+itoa(pos.Line))
						}
					}
				case *ast.TypeSpec:
					if x.Assign != 0 {
						return true
					}
					switch t := x.Type.(type) {
					case *ast.StructType:
						if records && exported(x.Name.Name) && exportedField(t) {
							c := cs["app-exported-record"]
							c.N++
							c.Names = append(c.Names, x.Name.Name)
						}
					case *ast.Ident:
						if basic[t.Name] && t.Name != "error" && t.Name != "any" {
							c := cs["app-named-primitive"]
							c.N++
							c.Names = append(c.Names, x.Name.Name)
						}
					}
				case *ast.AssignStmt:
					if x.Tok == token.DEFINE || adapter {
						return true
					}
					for _, l := range x.Lhs {
						if sel, ok := l.(*ast.SelectorExpr); ok && (sel.Sel.Name == "DoneAt" || sel.Sel.Name == "DoneBy") {
							c := cs["app-lifecycle-write"]
							c.N++
							pos := f.pos(sel)
							c.Names = append(c.Names, pos.Filename+":"+itoa(pos.Line))
						}
					}
				case *ast.FuncDecl:
					return true
				}
				return true
			})
		}
		for _, rule := range counted {
			if cs[rule].N > 0 {
				out = append(out, *cs[rule])
			}
		}
	}
	return out, nil
}

// exportedField says whether a struct type has an exported field, an embedded exported type included.
func exportedField(st *ast.StructType) bool {
	for _, f := range st.Fields.List {
		if len(f.Names) == 0 {
			if _, name := baseType(f.Type); exported(name) {
				return true
			}
		}
		for _, n := range f.Names {
			if exported(n.Name) {
				return true
			}
		}
	}
	return false
}
