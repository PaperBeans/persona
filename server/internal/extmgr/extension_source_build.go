package extmgr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/i18n"
)

func newSourceExtension(id, name, dir string, stdout io.Writer) error {
	if name == "" {
		name = id
	}
	if len([]rune(name)) > 40 || strings.ContainsAny(name, "\"\\\n") {
		return errors.New("name must be at most 40 characters, without quotes, backslashes or newlines")
	}
	if dir == "" {
		dir = id
	}
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		return errors.New("new extension directory must be empty")
	}
	files, err := bundleFiles()
	if err != nil {
		return err
	}
	if err := copySDK(dir, "ts", files); err != nil {
		return err
	}
	// The build replaces this digest before a package can pass validation.
	m := extension.Manifest{ManifestVersion: 1, ID: id, Name: name, Version: "0.1.0", Engines: extension.Engines{ExtensionProtocol: extension.Protocol}, Runtime: &extension.Runtime{Mode: extension.RuntimeDeno, Source: &extension.SourceRuntime{Entry: "src/main.ts", Bundle: "bundle/main.mjs", SHA256: fmt.Sprintf("%064d", 0)}}, Contributes: map[string]json.RawMessage{"ui": json.RawMessage(`[{"id":"` + id + `-today","title":"Example","placement":"extension_page"}]`)}}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := writeNew(filepath.Join(dir, extension.ManifestFile), b); err != nil {
		return err
	}
	program := fmt.Sprintf("export default { id: %q, version: \"0.1.0\", ui: { %q: { render: () => ({sections: []}) } }, namespaces: { example: (_method: string, params: unknown) => params } };\n", id, id+"-today")
	if err := writeNew(filepath.Join(dir, "src", "main.ts"), []byte(program)); err != nil {
		return err
	}
	fmt.Fprintln(stdout, i18n.T(i18n.Terminal(), "extension-source.created"))
	return nil
}

func (p *devProject) buildSource(ctx context.Context, stderr io.Writer) error {
	s := p.manifest.Runtime.Source
	root, err := filepath.EvalSymlinks(p.dir)
	if err != nil {
		return err
	}
	entry, err := extension.SourceFile(root, s.Entry)
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(p.dir, ".source-build-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	out := filepath.Join(tmp, "bundle.mjs")
	cmd := exec.CommandContext(ctx, "bun", "build", "--target=browser", "--format=esm", "--minify", "--no-macros", entry, "--outfile", out)
	cmd.Dir = p.dir
	cmd.Stdout = stderr
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("source bundling failed: %w", err)
	}
	st, err := os.Stat(out)
	if err != nil {
		return err
	}
	if st.Size() <= 0 || st.Size() > extension.SourceBundleMost {
		return errors.New("source artifact exceeds size budget")
	}
	b, err := os.ReadFile(out)
	if err != nil {
		return err
	}
	if err := extension.CheckSelfContainedContext(ctx, b); err != nil {
		return err
	}
	// No symlink in the output directory may redirect an offline build outside the project.
	parent := filepath.Dir(filepath.Join(root, filepath.FromSlash(s.Bundle)))
	if err := makeSourceOutputDir(root, filepath.Dir(s.Bundle)); err != nil {
		return err
	}
	real, err := filepath.EvalSymlinks(parent)
	if err != nil || real != parent {
		return errors.New("source output directory must not traverse symlinks")
	}
	final := filepath.Join(parent, filepath.Base(s.Bundle))
	if st, err := os.Lstat(final); err == nil && !st.Mode().IsRegular() {
		return errors.New("source output must be regular")
	}
	if err := os.WriteFile(final, b, 0o644); err != nil {
		return err
	}
	h := sha256.Sum256(b)
	s.SHA256 = hex.EncodeToString(h[:])
	raw, err := os.ReadFile(extension.ManifestPath(p.dir))
	if err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	fields["runtime"], err = json.Marshal(p.manifest.Runtime)
	if err != nil {
		return err
	}
	raw, err = json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(extension.ManifestPath(p.dir), append(raw, '\n'), 0o644)
}

func makeSourceOutputDir(root, rel string) error {
	if rel == "." {
		return nil
	}
	if !filepath.IsLocal(rel) || filepath.ToSlash(filepath.Clean(rel)) != rel || strings.Contains(rel, "\\") {
		return errors.New("source output directory is not contained")
	}
	p := root
	for _, part := range strings.Split(rel, "/") {
		p = filepath.Join(p, part)
		st, err := os.Lstat(p)
		if os.IsNotExist(err) {
			if err := os.Mkdir(p, 0o755); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return errors.New("source output directory must not traverse symlinks")
		}
	}
	return nil
}
