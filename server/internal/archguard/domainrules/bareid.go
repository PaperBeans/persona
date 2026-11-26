package domainrules

import (
	"encoding/json"
	"go/ast"
	"go/token"
	"strings"
)

// wireBaselineFile is the wire-types ratchet's baseline, from the repository root (scripts/ci/wire_types_ratchet.py):
// the names contract-bare-id lets stand.
const wireBaselineFile = "scripts/ci/wire_types_baseline.json"

// ParseBareIDs reads the names of the wire-types baseline's bare-id counts: the contract packages' ("metrics"), every
// non-test package's under server/internal and the SDK's wire ("wide", so a field lifted into a contract package from
// a Core package or a module is neutral) and the exceptions that carry a PR reference ("exceptions"), as
// "<Type>.<Field>" with no package.
func ParseBareIDs(text string) (map[string]bool, error) {
	var data struct {
		Metrics    map[string]map[string]json.RawMessage `json:"metrics"`
		Wide       map[string]map[string]json.RawMessage `json:"wide"`
		Exceptions map[string]map[string]json.RawMessage `json:"exceptions"`
	}
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for _, m := range []map[string]map[string]json.RawMessage{data.Metrics, data.Wide, data.Exceptions} {
		for name := range m["bare-id"] {
			names[name] = true
		}
	}
	return names, nil
}

// WithBareIDs turns contract-bare-id on: a bare ID field of a contract package whose "<Type>.<Field>" is not in names
// fails. Without it (nil) the rule does not run.
func (idx Index) WithBareIDs(names map[string]bool) Index {
	idx.bareIDs = names
	return idx
}

// BareIDs is contract-bare-id: in a contract package's production files, a struct field of plain string or []string
// whose name ends in ID, Id or IDs and whose "<Type>.<Field>" (named as the wire-types ratchet's bare-id, nested
// anonymous structs included) the wire-types baseline does not hold. A field that moved or was lifted into a contract
// package keeps its name, and so passes; one written anew, or renamed, fails.
func bareIDs(fs *findings, pkgs []*Pkg, idx Index) error {
	if idx.bareIDs == nil {
		return nil
	}
	for _, p := range pkgs {
		if !idx.contracts[p.Path] {
			continue
		}
		for _, f := range p.Files {
			if f.Test {
				continue
			}
			if err := f.parse(); err != nil {
				return err
			}
			for _, d := range f.ast.Decls {
				gen, ok := d.(*ast.GenDecl)
				if !ok || gen.Tok != token.TYPE {
					continue
				}
				for _, spec := range gen.Specs {
					if ts, ok := spec.(*ast.TypeSpec); ok {
						if st, ok := ts.Type.(*ast.StructType); ok {
							bareIDFields(fs, f, p, idx, ts.Name.Name, st)
						}
					}
				}
			}
		}
	}
	return nil
}

func bareIDFields(fs *findings, f *File, p *Pkg, idx Index, prefix string, st *ast.StructType) {
	for _, field := range st.Fields.List {
		for _, id := range field.Names {
			name := prefix + "." + id.Name
			if inner, ok := field.Type.(*ast.StructType); ok {
				bareIDFields(fs, f, p, idx, name, inner)
			}
			if !bareIDType(field.Type) || !idSuffix(id.Name) || idx.bareIDs[name] {
				continue
			}
			fs.add(f.pos(id), "contract-bare-id", p.Rel, name+" is a plain "+types(field.Type)+" id in a contract package")
		}
	}
}

func idSuffix(name string) bool {
	return strings.HasSuffix(name, "ID") || strings.HasSuffix(name, "Id") || strings.HasSuffix(name, "IDs")
}

// bareIDType says whether t is string or []string.
func bareIDType(t ast.Expr) bool {
	if at, ok := t.(*ast.ArrayType); ok && at.Len == nil {
		t = at.Elt
	}
	id, ok := t.(*ast.Ident)
	return ok && id.Name == "string"
}

func types(t ast.Expr) string {
	if _, ok := t.(*ast.ArrayType); ok {
		return "[]string"
	}
	return "string"
}
