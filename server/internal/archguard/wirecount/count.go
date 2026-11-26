package wirecount

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Metrics lists every metric, in output order.
var Metrics = []string{
	"bare-enum-state", "bare-enum-kind", "bare-enum-type", "bare-enum-role", "bare-id", "bare-error", "map-any",
	"raw-json", "ptr-time", "ptr-bool", "ptr-error-string", "inline-event", "write-json-map", "literal-call",
}

// HandlerDirs are the handler-layer packages, relative to server/.
var HandlerDirs = []string{"internal/hub", "internal/assistant", "internal/devices", "internal/extmgr"}

// Counts is metric -> name -> occurrences.
type Counts map[string]map[string]int

func newCounts() Counts {
	c := Counts{}
	for _, m := range Metrics {
		c[m] = map[string]int{}
	}
	return c
}

func (c Counts) add(metric, name string) { c[metric][name]++ }

// CountFields adds the field metrics of one parsed file.
func CountFields(c Counts, f *ast.File) {
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			ts := spec.(*ast.TypeSpec)
			if st, ok := ts.Type.(*ast.StructType); ok {
				countStruct(c, ts.Name.Name, st)
			}
		}
	}
}

func countStruct(c Counts, prefix string, st *ast.StructType) {
	for _, field := range st.Fields.List {
		for _, ident := range field.Names {
			name := prefix + "." + ident.Name
			if inner, ok := field.Type.(*ast.StructType); ok {
				countStruct(c, name, inner)
			}
			classify(c, name, ident.Name, field.Type)
		}
	}
}

func classify(c Counts, name, field string, typ ast.Expr) {
	switch {
	case isIdent(typ, "string"):
		switch {
		case hasSuffix(field, "State", "Status", "Phase"):
			c.add("bare-enum-state", name)
		case hasSuffix(field, "Kind"):
			c.add("bare-enum-kind", name)
		case hasSuffix(field, "Type"):
			c.add("bare-enum-type", name)
		case hasSuffix(field, "Role"):
			c.add("bare-enum-role", name)
		}
		if hasSuffix(field, "ID", "Id", "IDs") {
			c.add("bare-id", name)
		}
		if hasSuffix(field, "Error") {
			c.add("bare-error", name)
		}
	case isSliceOf(typ, "string"):
		if hasSuffix(field, "ID", "Id", "IDs") {
			c.add("bare-id", name)
		}
	}
	if star, ok := typ.(*ast.StarExpr); ok {
		switch {
		case isSelector(star.X, "time", "Time"):
			c.add("ptr-time", name)
		case isIdent(star.X, "bool"):
			c.add("ptr-bool", name)
		case isIdent(star.X, "string") && hasSuffix(field, "Error"):
			c.add("ptr-error-string", name)
		}
	}
	if holds(typ, isMapAny) {
		c.add("map-any", name)
	}
	if holds(typ, func(e ast.Expr) bool { return isSelector(e, "json", "RawMessage") }) {
		c.add("raw-json", name)
	}
}

func hasSuffix(s string, suffixes ...string) bool {
	for _, x := range suffixes {
		if strings.HasSuffix(s, x) {
			return true
		}
	}
	return false
}

func isIdent(e ast.Expr, name string) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == name
}

func isSliceOf(e ast.Expr, elem string) bool {
	at, ok := e.(*ast.ArrayType)
	return ok && at.Len == nil && isIdent(at.Elt, elem)
}

func isSelector(e ast.Expr, pkg, name string) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg
}

func isAny(e ast.Expr) bool {
	if isIdent(e, "any") {
		return true
	}
	it, ok := e.(*ast.InterfaceType)
	return ok && (it.Methods == nil || len(it.Methods.List) == 0)
}

func isMapAny(e ast.Expr) bool {
	m, ok := e.(*ast.MapType)
	return ok && isIdent(m.Key, "string") && isAny(m.Value)
}

// holds reports whether a type expression is, or is built from (pointer, slice, array, map, channel), a type for
// which match is true. It does not look into struct literals: their fields count on their own.
func holds(e ast.Expr, match func(ast.Expr) bool) bool {
	if match(e) {
		return true
	}
	switch t := e.(type) {
	case *ast.StarExpr:
		return holds(t.X, match)
	case *ast.ArrayType:
		return holds(t.Elt, match)
	case *ast.MapType:
		return holds(t.Key, match) || holds(t.Value, match)
	case *ast.ChanType:
		return holds(t.Value, match)
	case *ast.ParenExpr:
		return holds(t.X, match)
	}
	return false
}

// CountHandlers adds the handler-layer metrics of one parsed file.
func CountHandlers(c Counts, f *ast.File) {
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CompositeLit:
			if x.Type != nil && isMapAny(x.Type) {
				for _, elt := range x.Elts {
					kv, ok := elt.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					if key, ok := stringLit(kv.Key); ok && key == "type" {
						value, ok := stringLit(kv.Value)
						if !ok {
							value = "<dynamic>"
						}
						c.add("inline-event", value)
					}
				}
			}
		case *ast.CallExpr:
			countCall(c, x)
		}
		return true
	})
}

func countCall(c Counts, call *ast.CallExpr) {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		if fn.Name == "writeJSON" {
			countWriteJSON(c, call)
		}
	case *ast.SelectorExpr:
		if fn.Sel.Name == "writeJSON" {
			countWriteJSON(c, call)
		}
		if fn.Sel.Name == "Call" && len(call.Args) >= 2 {
			if method, ok := stringLit(call.Args[1]); ok {
				c.add("literal-call", method)
			}
		}
	}
}

func countWriteJSON(c Counts, call *ast.CallExpr) {
	for _, arg := range call.Args {
		lit, ok := arg.(*ast.CompositeLit)
		if !ok || lit.Type == nil || !isMapAny(lit.Type) {
			continue
		}
		var keys []string
		for _, elt := range lit.Elts {
			if kv, ok := elt.(*ast.KeyValueExpr); ok {
				if key, ok := stringLit(kv.Key); ok {
					keys = append(keys, key)
					continue
				}
			}
			keys = append(keys, "?")
		}
		sort.Strings(keys)
		c.add("write-json-map", "{"+strings.Join(keys, ",")+"}")
		return
	}
}

func stringLit(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	return s, err == nil
}

// parseDir parses the non-test .go files of one directory (and, with recurse, below it, skipping testdata).
func parseDir(dir string, recurse bool, visit func(*ast.File)) error {
	return filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != dir && (!recurse || d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		visit(f)
		return nil
	})
}

// ContractDirs reads contracts.txt: one package per line, comments with #; an SDK import path maps to sdk/go.
func ContractDirs(serverRoot string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(serverRoot, "internal/archguard/testdata/contracts.txt"))
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if rest, ok := strings.CutPrefix(line, "github.com/nwverse/persona/sdk/go/"); ok {
			dirs = append(dirs, filepath.Join(serverRoot, "..", "sdk", "go", rest))
		} else {
			dirs = append(dirs, filepath.Join(serverRoot, line))
		}
	}
	return dirs, nil
}

// FieldMetrics are the metrics CountFields adds, in output order; Wide counts only these.
var FieldMetrics = Metrics[:11]

// Wide counts the field metrics over every non-test package under serverRoot/internal (modules included, testdata
// skipped) and the contract packages outside it (the SDK's wire package). The ratchet uses it to tell a weak field
// that is new from one that already existed outside the contract packages and was moved into one: lifting a type
// from a Core package into a contract package (the first step of moving a module out) keeps its wide count.
func Wide(serverRoot string) (Counts, error) {
	c := Counts{}
	for _, m := range FieldMetrics {
		c[m] = map[string]int{}
	}
	internal := filepath.Clean(filepath.Join(serverRoot, "internal"))
	if err := parseDir(internal, true, func(f *ast.File) { CountFields(c, f) }); err != nil {
		return nil, err
	}
	dirs, err := ContractDirs(serverRoot)
	if err != nil {
		return nil, err
	}
	for _, dir := range dirs {
		dir = filepath.Clean(dir)
		if dir == internal || strings.HasPrefix(dir, internal+string(filepath.Separator)) {
			continue
		}
		if err := parseDir(dir, false, func(f *ast.File) { CountFields(c, f) }); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// Count runs every metric over the tree under serverRoot.
func Count(serverRoot string) (Counts, error) {
	c := newCounts()
	dirs, err := ContractDirs(serverRoot)
	if err != nil {
		return nil, err
	}
	for _, dir := range dirs {
		if err := parseDir(dir, false, func(f *ast.File) { CountFields(c, f) }); err != nil {
			return nil, err
		}
	}
	for _, dir := range HandlerDirs {
		if err := parseDir(filepath.Join(serverRoot, dir), true, func(f *ast.File) { CountHandlers(c, f) }); err != nil {
			return nil, err
		}
	}
	return c, nil
}
