package archguard

import (
	"bufio"
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// Where a panic may be recovered (docs/server/architecture.md "Validated and sealed"). A programmer error is a
// defect (domainkit/defect): it stops the operation, and only a listed site recovers it, through defect.Catch. Both
// lists are permission lists: a production file that is not listed fails, and so does a line whose file no longer
// does what the line permits, since a stale line would be a standing permission.
//
//   - testdata/recover_sites.txt (rule recover-site): every production .go file under server/ that calls the builtin
//     recover(), with its reason. defect.Catch's own file is one of them.
//   - testdata/catch_sites.txt (rule catch-site): every production .go file that refers to defect.Catch, which
//     recovers only when it is deferred, with its reason.
//
// Tests (_test.go) and testdata, vendor, node_modules, hidden and "_" directories are not read. The checks read syntax:
// a call of recover that the file does not declare itself, and a selector Catch on the name the file imports
// domainkit/defect under. Every message has the domain guards' form: RULE <id> <what>. WHY ... FIX ... GOOD <path>.
// and the doc section.

const (
	defectSitesDoc = `see docs/server/architecture.md "Domain model" ("Failures")`
	defectImport   = "github.com/nwverse/persona/server/internal/domainkit/defect"
	// defectSitesGood is the one catch site: what a site list's messages name as done right.
	defectSitesGood = "server/internal/assistant/kernel/seam/dispatch.go (Registry.dispatch)"
)

// A siteList is one of the two permission lists and what its guard says about it.
type siteList struct {
	rule string // the rule id every message about the list starts with
	name string // the list's file under testdata/
	does string // what a file the guard finds does
	gone string // what the file of a stale line no longer does
	why  string // why a file the guard finds must be listed
	fix  string // what fixes a file the guard finds that is not listed
}

var (
	recoverSites = siteList{
		rule: "recover-site",
		name: "recover_sites.txt",
		does: "calls recover()",
		gone: "recovers",
		why: "defects must not be swallowed in new places: a recover() outside the listed sites can hide a programmer " +
			"error that should stop its operation",
		fix: "raise a defect (defect.Raise) and let the kernel's dispatch catch it, or return an error; a boundary " +
			"that must outlive a panic of code it does not own gets a line in testdata/recover_sites.txt with its reason",
	}
	catchSites = siteList{
		rule: "catch-site",
		name: "catch_sites.txt",
		does: "refers to defect.Catch",
		gone: "refers to defect.Catch",
		why: "defects must not be swallowed in new places: a catch site is a boundary where a programmer error fails " +
			"only its own request, and a new one needs a reviewer to agree that it is one",
		fix: "register the routes through the kernel (kernel/seam), whose dispatch catches a defect, or add a line to " +
			"testdata/catch_sites.txt with the reason the file is a boundary of its own",
	}
)

// message is a finding about the list: RULE <id> <what>. WHY ... FIX ... GOOD ... and the doc section.
func (l siteList) message(what, why, fix string) string {
	return "RULE " + l.rule + " " + what + ". WHY " + why + ". FIX " + fix + ". GOOD " + defectSitesGood + ". " +
		defectSitesDoc
}

func TestRecoverOnlyAtListedSites(t *testing.T) {
	found := scanSites(t, serverDir, []byte("recover"), callsRecover)
	for _, p := range recoverSites.judge(found, readSites(t, recoverSites)) {
		t.Error(p)
	}
}

func TestCatchOnlyAtListedSites(t *testing.T) {
	found := scanSites(t, serverDir, []byte("domainkit/defect"), refersToCatch)
	for _, p := range catchSites.judge(found, readSites(t, catchSites)) {
		t.Error(p)
	}
}

// readSites reads the list l from testdata; what is wrong with its lines fails the test.
func readSites(t *testing.T, l siteList) map[string]string {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", l.name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sites, problems, err := l.parse(f)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		t.Error(p)
	}
	return sites
}

// parse reads a site list: "<file under server/> <reason>" per line, "#" starting a comment line. A line with no
// reason and a file listed twice are problems.
func (l siteList) parse(r io.Reader) (map[string]string, []string, error) {
	sites := map[string]string{}
	var problems []string
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		file, reason, _ := strings.Cut(line, " ")
		at := "testdata/" + l.name + ":" + strconv.Itoa(n)
		_, twice := sites[file]
		switch reason = strings.TrimSpace(reason); {
		case reason == "":
			problems = append(problems, l.message(at+" lists "+file+" without a reason",
				"the reason is what a reviewer weighs before the list grows", "write why the file is a site after its "+
					"path, on the same line"))
		case twice:
			problems = append(problems, l.message(at+" lists "+file+" twice",
				"each file has one line and one reason", "delete one of its lines"))
		}
		sites[file] = reason
	}
	return sites, problems, sc.Err()
}

// judge is what is wrong between the files found and the list's lines: a file found but not listed, and a line whose
// file was not found.
func (l siteList) judge(found []string, listed map[string]string) []string {
	var problems []string
	for _, f := range found {
		if _, ok := listed[f]; !ok {
			problems = append(problems, l.message("server/"+f+" "+l.does+" but is not in "+l.name, l.why, l.fix))
		}
	}
	var stale []string
	for f := range listed {
		if !slices.Contains(found, f) {
			stale = append(stale, f)
		}
	}
	slices.Sort(stale)
	for _, f := range stale {
		problems = append(problems, l.message("testdata/"+l.name+" lists "+f+", which no longer "+l.gone,
			"a line is a standing permission, so a stale one would let a later change in that file do it again unreviewed",
			"delete its line"))
	}
	return problems
}

// scanSites is the production .go files under root (paths relative to it, sorted) for which match holds. Only files
// whose bytes contain prefilter are parsed.
func scanSites(t *testing.T, root string, prefilter []byte, match func(*ast.File) bool) []string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if path != root && (name == "testdata" || name == "vendor" || name == "node_modules" ||
				strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil || !bytes.Contains(src, prefilter) {
			return err
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, src, 0)
		if err != nil {
			return err
		}
		if match(f) {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			found = append(found, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(found)
	return found
}

// callsRecover says whether f calls the builtin recover: a call of the name recover that f does not declare itself
// (a method call such as m.recover() is a selector, not this name).
func callsRecover(f *ast.File) bool {
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			if id, ok := c.Fun.(*ast.Ident); ok && id.Name == "recover" && id.Obj == nil {
				found = true
			}
		}
		return !found
	})
	return found
}

// refersToCatch says whether f refers to defect.Catch, under whatever name f imports domainkit/defect.
func refersToCatch(f *ast.File) bool {
	name := ""
	for _, imp := range f.Imports {
		if p, err := strconv.Unquote(imp.Path.Value); err == nil && p == defectImport {
			name = "defect"
			if imp.Name != nil {
				name = imp.Name.Name
			}
		}
	}
	if name == "" || name == "_" {
		return false
	}
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		if s, ok := n.(*ast.SelectorExpr); ok && s.Sel.Name == "Catch" {
			if id, ok := s.X.(*ast.Ident); ok && id.Name == name && id.Obj == nil {
				found = true
			}
		}
		if id, ok := n.(*ast.Ident); ok && name == "." && id.Name == "Catch" && id.Obj == nil {
			found = true
		}
		return !found
	})
	return found
}

// The site checks on planted sources: what counts as a site, and that an unlisted site, a stale line, a line without
// a reason and a file listed twice each fail with a RULE <id> <what>. WHY ... FIX ... GOOD ... message whose GOOD path
// exists.
func TestDefectSiteChecks(t *testing.T) {
	parse := func(src string) *ast.File {
		t.Helper()
		f, err := parser.ParseFile(token.NewFileSet(), "planted.go", src, 0)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	for _, c := range []struct {
		src            string
		recover, catch bool
	}{
		{"package p\nfunc f() { defer func() { _ = recover() }() }\n", true, false},
		{"package p\nfunc f() { if recover() != nil {} }\n", true, false},
		{"package p\ntype m struct{}\nfunc (m) recover() error { return nil }\nfunc f(x m) { _ = x.recover() }\n", false, false},
		{"package p\nfunc f() { recover := func() any { return nil }; _ = recover() }\n", false, false},
		{"package p\nimport \"" + defectImport + "\"\nfunc f() { defer defect.Catch(nil) }\n", false, true},
		{"package p\nimport d \"" + defectImport + "\"\nfunc f() { catch := d.Catch; defer catch(nil) }\n", false, true},
		{"package p\nimport . \"" + defectImport + "\"\nfunc f() { defer Catch(nil) }\n", false, true},
		{"package p\nimport \"" + defectImport + "\"\nfunc f() { defect.Raise(\"op\", \"cause\") }\n", false, false},
		{"package p\nimport \"other/defect\"\nfunc f() { defer defect.Catch(nil) }\n", false, false},
	} {
		f := parse(c.src)
		if got := callsRecover(f); got != c.recover {
			t.Errorf("callsRecover = %v, want %v:\n%s", got, c.recover, c.src)
		}
		if got := refersToCatch(f); got != c.catch {
			t.Errorf("refersToCatch = %v, want %v:\n%s", got, c.catch, c.src)
		}
	}
	for _, l := range []siteList{recoverSites, catchSites} {
		got := l.judge([]string{"internal/a/a.go", "internal/b/b.go"},
			map[string]string{"internal/a/a.go": "a reason", "internal/c/c.go": "gone"})
		_, malformed, err := l.parse(strings.NewReader("# a comment\ninternal/a/a.go a reason\n\n" +
			"internal/b/b.go\ninternal/a/a.go again\n"))
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, malformed...)
		want := []string{
			"RULE " + l.rule + " server/internal/b/b.go " + l.does + " but is not in " + l.name + ". WHY " + l.why +
				". FIX " + l.fix + ". GOOD " + defectSitesGood + ". " + defectSitesDoc,
			"RULE " + l.rule + " testdata/" + l.name + " lists internal/c/c.go, which no longer " + l.gone + ". WHY ",
			"RULE " + l.rule + " testdata/" + l.name + ":4 lists internal/b/b.go without a reason. WHY ",
			"RULE " + l.rule + " testdata/" + l.name + ":5 lists internal/a/a.go twice. WHY ",
		}
		if len(got) != len(want) {
			t.Fatalf("%s: %d problems, want %d:\n%s", l.name, len(got), len(want), strings.Join(got, "\n"))
		}
		for i, g := range got {
			if !strings.HasPrefix(g, want[i]) {
				t.Errorf("%s: problem %d:\n got %s\nwant %s…", l.name, i, g, want[i])
			}
			if !strings.Contains(g, ". WHY ") || !strings.Contains(g, ". FIX ") ||
				!strings.Contains(g, ". GOOD "+defectSitesGood+". ") || !strings.HasSuffix(g, defectSitesDoc) {
				t.Errorf("%s: message without RULE/WHY/FIX/GOOD/citation: %s", l.name, g)
			}
		}
	}
	good, _, _ := strings.Cut(defectSitesGood, " (")
	if _, err := os.Stat(filepath.Join(serverDir, "..", filepath.FromSlash(good))); err != nil {
		t.Errorf("the site lists' GOOD path %s does not exist", good)
	}
}
