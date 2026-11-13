package extmgr

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// new → test → pack, on each template: what `personad extension …` does for someone writing an extension.
func TestNewTestPackTemplates(t *testing.T) {
	if testing.Short() {
		t.Skip("builds programs")
	}
	t.Setenv("PERSONA_LANG", "en") // what the commands print reads in the terminal's language (#5520)
	for _, template := range []string{"go", "ts"} {
		t.Run(template, func(t *testing.T) {
			tool := map[string]string{"go": "go", "ts": "bun"}[template]
			if _, err := exec.LookPath(tool); err != nil {
				t.Skipf("no %s here", tool)
			}
			dir := filepath.Join(t.TempDir(), "demo")
			var out bytes.Buffer
			if err := newExtension("demo-"+template, template, "演示", dir, &out); err != nil {
				t.Fatal(err)
			}
			if err := newExtension("demo-"+template, template, "演示", dir, &out); err == nil {
				t.Fatal("new into a directory that has files")
			}
			for _, f := range []string{"persona-extension.json", "README.md", ".gitignore"} {
				if b, err := os.ReadFile(filepath.Join(dir, f)); err != nil || strings.Contains(string(b), "{{") {
					t.Fatalf("%s: %v %q", f, err, b)
				}
			}
			sdkFile := map[string]string{"go": "sdk/go/ext/ext.go", "ts": "sdk/ts/src/index.ts"}[template]
			if _, err := os.Stat(filepath.Join(dir, sdkFile)); err != nil {
				t.Fatalf("the SDK is not copied in: %v", err)
			}
			if template == "go" {
				cmd := exec.Command("go", "test", "./...")
				cmd.Dir = dir
				if b, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("go test in the new extension: %v\n%s", err, b)
				}
			}
			ctx := context.Background()
			out.Reset()
			var errs bytes.Buffer
			if err := testExtension(ctx, dir, &out, &errs); errors.Is(err, errBunUnsigned) {
				t.Skipf("the bun here cannot build for this Mac: %v", err) // the machine's bun, not the template (#2555)
			} else if err != nil {
				t.Fatalf("test: %v\n%s\n%s", err, out.String(), errs.String())
			}
			if !strings.Contains(out.String(), "✓ Manifest and package (demo-"+template) || !strings.Contains(out.String(), "✓ Protocol: handshake") {
				t.Fatalf("test said: %s", out.String())
			}
			here := runtime.GOOS + "-" + runtime.GOARCH
			out.Reset()
			if err := packExtension(ctx, dir, map[string]string{"platforms": here}, &out, &errs); err != nil {
				t.Fatalf("pack: %v\n%s", err, errs.String())
			}
			names := tarNames(t, filepath.Join(dir, "dist", "demo-"+template+"-0.1.0.tar.gz"))
			want := []string{"demo-" + template + "-0.1.0/README.md", "demo-" + template + "-0.1.0/bin/demo-" + template + "-" + here,
				"demo-" + template + "-0.1.0/persona-extension.json", "demo-" + template + "-0.1.0/persona-plugin.json"}
			if !slices.Equal(names, want) {
				t.Fatalf("the package holds %v, want %v (no source, no SDK)", names, want)
			}
			if !strings.Contains(out.String(), `"sha256"`) {
				t.Fatalf("pack printed no index entry: %s", out.String())
			}
		})
	}
}

// The package carries the manifest under both names and without its tags: hubs from before the rename read only
// persona-plugin.json, and hubs from before tags refuse a manifest with a field they don't know.
func TestPackWritesTheManifestForOlderHubs(t *testing.T) {
	dir := t.TempDir()
	src := `{"manifest_version": 1, "id": "tagged", "name": "Tagged", "version": "1.0.0", "tags": ["示例", "开发"],
 "contributes": {"prompts": [{"id": "hi", "kind": "quick", "title": "Hi", "text": "Say hi."}]}}`
	if err := os.WriteFile(filepath.Join(dir, "persona-extension.json"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errs bytes.Buffer
	if err := packExtension(context.Background(), dir, nil, &out, &errs); err != nil {
		t.Fatalf("pack: %v\n%s", err, errs.String())
	}
	files := tarFiles(t, filepath.Join(dir, "dist", "tagged-1.0.0.tar.gz"))
	extensionJSON, pluginJSON := files["tagged-1.0.0/persona-extension.json"], files["tagged-1.0.0/persona-plugin.json"]
	if extensionJSON == "" || extensionJSON != pluginJSON {
		t.Fatalf("the two manifests: %q and %q", extensionJSON, pluginJSON)
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(extensionJSON), &fields); err != nil || fields["tags"] != nil || fields["id"] != "tagged" {
		t.Fatalf("packaged manifest %v: %v", fields, err)
	}
	if src, _ := os.ReadFile(filepath.Join(dir, "persona-extension.json")); !strings.Contains(string(src), `"tags"`) {
		t.Fatal("pack changed the source manifest")
	}
}

func tarFiles(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		files[h.Name] = string(b)
	}
	return files
}

func tarNames(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, h.Name)
	}
	slices.Sort(names)
	return names
}

func TestNewRefusesBadInput(t *testing.T) {
	var out bytes.Buffer
	for _, c := range [][3]string{{"Bad ID", "go", "x"}, {"ok", "rust", "x"}, {"ok", "go", `a"b`}} {
		if err := newExtension(c[0], c[1], c[2], filepath.Join(t.TempDir(), "x"), &out); err == nil {
			t.Errorf("new %v: no error", c)
		}
	}
	// The refusals read in the terminal's language (#5520).
	for _, tt := range []struct{ lang, template, want string }{
		{"en", "rust", `The templates are go and ts; there is no "rust"`},
		{"ja", "rust", `テンプレートは go と ts だけです（"rust" はありません）`},
		{"zh-Hans", "rust", `模板只有 go 和 ts，没有 "rust"`},
	} {
		t.Setenv("PERSONA_LANG", tt.lang)
		if err := newExtension("ok", tt.template, "x", filepath.Join(t.TempDir(), "x"), &out); err == nil || err.Error() != tt.want {
			t.Errorf("%s: %v, want %q", tt.lang, err, tt.want)
		}
	}
}

// A new extension's next steps, in the terminal's language, with the commands as they are typed.
func TestNewPrintsNextStepsInTheTerminalLanguage(t *testing.T) {
	t.Setenv("PERSONA_LANG", "en")
	dir := filepath.Join(t.TempDir(), "demo")
	var out bytes.Buffer
	if err := newExtension("demo-steps", "go", "Demo", dir, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Wrote the extension demo-steps in " + dir + " (go template). Next:",
		"  go test ./...                 unit tests and conformance checks\n",
		"personad extension dev", "try it on this machine’s hub", "personad extension pack"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("no %q in:\n%s", want, out.String())
		}
	}
	if _, err := openProject(filepath.Join(t.TempDir(), "nothing")); err == nil || !strings.HasPrefix(err.Error(), "There is no persona-extension.json in ") {
		t.Errorf("no manifest: %v", err)
	}
}

// An extension of an extension repository (extensions/<id>/, one go.mod at the repository's root) is built as Go; a
// TypeScript one there is still TypeScript, and the search for a go.mod stops at the repository.
func TestOpenProjectInARepository(t *testing.T) {
	repo := t.TempDir()
	write := func(path, s string) {
		p := filepath.Join(repo, path)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manifest := func(id string) string {
		return `{"manifest_version": 1, "id": "` + id + `", "name": "` + id + `", "version": "1.0.0", "engines": {"extension_protocol": 1},
 "runtime": {"command": "bin/` + id + `"}, "contributes": {"ui": [{"id": "` + id + `", "title": "x", "placement": "nav_page"}]}}`
	}
	write("go.mod", "module example.com/exts\n")
	write("extensions/gox/persona-extension.json", manifest("gox"))
	write("extensions/tsx/persona-extension.json", manifest("tsx"))
	write("extensions/tsx/src/main.ts", "")
	for id, kind := range map[string]string{"gox": "go", "tsx": "ts"} {
		p, err := openProject(filepath.Join(repo, "extensions", id))
		if err != nil || p.kind != kind {
			t.Fatalf("%s: %+v %v", id, p, err)
		}
	}
	write("extensions/decl/persona-extension.json", `{"manifest_version": 1, "id": "decl", "name": "Decl", "version": "1.0.0",
 "contributes": {"providers": [{"id": "decl", "name": "Decl", "base_url": "https://example.com"}]}}`)
	if p, err := openProject(filepath.Join(repo, "extensions", "decl")); err != nil || p.kind != "" {
		t.Fatalf("a declarative extension in a Go repository: %+v %v", p, err)
	}
	// a repository of its own (.git) without a go.mod: not Go, even if a directory above has one
	write("inner/.git/HEAD", "ref: refs/heads/main\n")
	write("inner/extensions/x/persona-extension.json", manifest("x"))
	if _, err := openProject(filepath.Join(repo, "inner", "extensions", "x")); err == nil {
		t.Fatal("a go.mod above the repository is not the extension's")
	}
}
