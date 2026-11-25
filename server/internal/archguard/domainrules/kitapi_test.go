package domainrules

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestKitAPIIsNeeded holds kitAPI to what the kit's listed API needs: the kit gives no finding with it, and each rule
// on each line, taken off on its own, gives a finding of that rule in that package (only that line changed, so the
// finding is the one it held back). So no line outlives the code that needed it, and the kit runs every other core
// rule.
func TestKitAPIIsNeeded(t *testing.T) {
	files := map[string]string{}
	root := filepath.Join("..", "..", "domainkit")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		if strings.HasSuffix(path, "_test.go") && filepath.Base(path) != "rules_test.go" {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		text, err := os.ReadFile(path)
		files["internal/domainkit/"+filepath.ToSlash(rel)] = string(text)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	const module = "github.com/nwverse/persona/server"
	findings := func() []Finding {
		t.Helper()
		pkgs, err := LoadFiles(module, files)
		if err != nil {
			t.Fatal(err)
		}
		all, _, err := Run(pkgs, NewIndex(module, pkgs, nil), "")
		if err != nil {
			t.Fatal(err)
		}
		return all
	}
	if got := findings(); len(got) != 0 {
		t.Fatalf("the kit has findings with kitAPI:\n%s", findingLines(got))
	}
	listed := kitAPI
	t.Cleanup(func() { kitAPI = listed })
	for key, ids := range listed {
		pkg, _, _ := strings.Cut(key, ".")
		for _, id := range ids {
			kitAPI = map[string][]string{}
			for k, v := range listed {
				kitAPI[k] = v
			}
			kitAPI[key] = slices.DeleteFunc(slices.Clone(ids), func(r string) bool { return r == id })
			needed := false
			for _, f := range findings() {
				needed = needed || f.Rule == id && f.Key == pkg
			}
			if !needed {
				t.Errorf("kitAPI's %s %s is not needed: without it the kit gives no %s finding; take it off", key, id, id)
			}
		}
	}
}

// findingLines is findings one per line.
func findingLines(fs []Finding) string {
	var lines []string
	for _, f := range fs {
		lines = append(lines, f.String())
	}
	return strings.Join(lines, "\n")
}
