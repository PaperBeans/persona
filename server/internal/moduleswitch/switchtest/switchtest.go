// Package switchtest checks a composition root against the run-time module switch (moduleswitch): every provider
// seam a module fills is present while the module is on and absent once it is switched off, and every call a root's
// init makes is such a seam. internal/modules/all and internal/modules/all/node run it over their modules.
package switchtest

import (
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/moduleswitch"
)

// Check runs both checks for the composition root in dir (its non-test files) and the module ids this build keeps
// (built). own lists the root's own calls in a file of no module (kernel.Provide, which hands the kernel the list;
// the kernel follows the switch itself). binaryOnly lists calls (`pkg.Func`) that add a subcommand to the binary
// (`personad office`): a command run as its own process has no hub to read the switch from, so only a build without
// the module removes it; an entry that is no longer called fails.
func Check(t *testing.T, dir string, built, own, binaryOnly []string) {
	t.Helper()
	seams := moduleswitch.Seams()
	for _, s := range seams {
		if !slices.Contains(built, s.Module) {
			t.Errorf("port %s was provided by %q, which is not a module of this build (%v)", s.Port, s.Module, built)
		}
	}
	for _, id := range built {
		var others []string
		for _, o := range built {
			if o != id {
				others = append(others, o)
			}
		}
		for _, s := range seams {
			if s.Module != id {
				continue
			}
			if !s.Present(moduleswitch.Set{}) {
				t.Errorf("%s: port %s hands out nothing with every module on", id, s.Port)
			}
			if s.Present(moduleswitch.Off(id)) {
				t.Errorf("%s: port %s still hands out the module's provider with %s switched off", id, s.Port, id)
			}
			if !s.Present(moduleswitch.Off(others...)) {
				t.Errorf("%s: port %s hands out nothing when only the other modules are off", id, s.Port)
			}
		}
	}
	used := map[string]bool{}
	for _, c := range rootCalls(t, dir) {
		if c.module == "" {
			if !slices.Contains(own, c.call) {
				t.Errorf("%s: init calls %s, but the file has no persona_without_<id> constraint naming its module", c.file, c.call)
			}
			continue
		}
		used[c.call] = true // whatever the build: the list is the source's
		if !slices.Contains(built, c.module) {
			continue // a file this build leaves out: its ports are not provided
		}
		if slices.Contains(binaryOnly, c.call) {
			continue
		}
		if !hasSeam(seams, c.module, c.pkg, c.call) {
			t.Errorf("%s: %s's init calls %s, which records no seam of %s that follows the switch (moduleswitch.Port)", c.file, c.module, c.call, c.module)
		}
	}
	for _, p := range binaryOnly {
		if !used[p] {
			t.Errorf("binary-only entry %s is no longer called by the root: remove it", p)
		}
	}
}

// hasSeam: a seam of module whose port is the package pkg (its name, or the end of its path), or, for a package of
// several ports, the call itself (pkg.Func).
func hasSeam(seams []moduleswitch.Seam, module, pkg, call string) bool {
	for _, s := range seams {
		if s.Module != module {
			continue
		}
		if s.Port == pkg || strings.HasSuffix(s.Port, "/"+pkg) || s.Port == call || strings.HasSuffix(s.Port, "/"+call) {
			return true
		}
	}
	return false
}

type rootCall struct {
	file, module, pkg, call string
}

// rootCalls is every call statement in the inits of dir's non-test files, with the module id of the file's build
// constraint (!persona_without_<tag>, the tag's _ an id's -). `modules = append(...)` is an assignment, not listed.
func rootCalls(t *testing.T, dir string) []rootCall {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []rootCall
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		module := fileModule(f)
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Name.Name != "init" || fn.Recv != nil || fn.Body == nil {
				continue
			}
			for _, st := range fn.Body.List {
				es, ok := st.(*ast.ExprStmt)
				if !ok {
					continue
				}
				call, ok := es.X.(*ast.CallExpr)
				if !ok {
					continue
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					continue
				}
				pkg, ok := sel.X.(*ast.Ident)
				if !ok {
					continue
				}
				out = append(out, rootCall{file: name, module: module, pkg: pkg.Name, call: pkg.Name + "." + sel.Sel.Name})
			}
		}
	}
	return out
}

// fileModule is the id in the file's //go:build line's !persona_without_<tag>, "" if none.
func fileModule(f *ast.File) string {
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			if !constraint.IsGoBuild(c.Text) {
				continue
			}
			expr, err := constraint.Parse(c.Text)
			if err != nil {
				return ""
			}
			var id string
			var walk func(constraint.Expr, bool)
			walk = func(e constraint.Expr, neg bool) {
				switch x := e.(type) {
				case *constraint.NotExpr:
					walk(x.X, !neg)
				case *constraint.AndExpr:
					walk(x.X, neg)
					walk(x.Y, neg)
				case *constraint.OrExpr:
					walk(x.X, neg)
					walk(x.Y, neg)
				case *constraint.TagExpr:
					if tag, ok := strings.CutPrefix(x.Tag, "persona_without_"); ok && neg && id == "" {
						id = strings.ReplaceAll(tag, "_", "-")
					}
				}
			}
			walk(expr, false)
			return id
		}
	}
	return ""
}
