package archguard

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// scaffoldDir is scripts/new_domain_slice.py's golden: what it writes with every flag but --shared, in a tree laid
// out like server/. scripts/test/test_new_domain_slice.py writes it again byte for byte, then compiles, vets and tests
// it beside the other flag combinations that script lists.
const scaffoldDir = "testdata/scaffold"

// TestScaffoldGoldenPasses holds what the scaffold writes to the domain rules (docs/server/architecture.md "Domain
// model"): the golden, beside the domain kit, gives no finding and no count, so a package started with the scaffold
// passes its own rules_test.go hook from its first line.
func TestScaffoldGoldenPasses(t *testing.T) {
	files := map[string]string{}
	err := filepath.WalkDir(scaffoldDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		rel, err := filepath.Rel(scaffoldDir, path)
		if err != nil {
			return err
		}
		text, err := os.ReadFile(path)
		files[filepath.ToSlash(rel)] = string(text)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatalf("%s holds no Go files: write it with SCAFFOLD_GOLDEN_UPDATE=1 python3 -B "+
			"scripts/test/test_new_domain_slice.py", scaffoldDir)
	}
	got, findings, _ := domainCase(t, files, nil)
	for _, f := range findings {
		t.Errorf("%s:%d: %s", f.Pos.Filename, f.Pos.Line, f.Msg)
	}
	if len(got) > len(findings) {
		t.Errorf("the golden adds to the application counts: %q", got)
	}
}
