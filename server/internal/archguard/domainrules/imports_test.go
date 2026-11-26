package domainrules

import (
	"go/parser"
	"go/token"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// allowedImports is everything this package's non-test files may import (doc.go says so): the parser and the file
// system, so the rules read source alone and every package's hook can link them without pulling a dependency in.
var allowedImports = []string{"encoding/json", "go/ast", "go/parser", "go/token", "os", "path/filepath", "strings", "testing"}

// TestImportsStayListed holds the package's own non-test files to allowedImports, so a convenience import (maps,
// sort, strconv) shows up here rather than drifting past the package comment.
func TestImportsStayListed(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for _, imp := range f.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(allowedImports, path) {
				t.Errorf("%s imports %s: domainrules imports only %s", fset.Position(imp.Pos()), path,
					strings.Join(allowedImports, ", "))
			}
		}
	}
	if checked == 0 {
		t.Fatal("no non-test .go file read: the test runs in the package's own directory")
	}
}
