package node

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

type lcuTarEntry struct {
	name, body, link string
	mode             int64
	kind             byte
}

func lcuTestStage(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func lcuTestArchive(t *testing.T, entries []lcuTarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Mode: e.mode, Typeflag: e.kind, Linkname: e.link}
		if e.kind == tar.TypeReg || e.kind == tar.TypeRegA {
			h.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Size > 0 {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type lcuManifestEntry struct {
	Path   string `json:"path"`
	Mode   uint32 `json:"mode,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	Link   string `json:"link,omitempty"`
}

// This synthetic layout models the file modes, link text and initially dangling link checked by the release manifest.
func lcuLayoutArchive(t *testing.T) []byte {
	t.Helper()
	files := []lcuTarEntry{
		{name: "bundle/scripts/install.sh", mode: 0o755, body: "#!/bin/sh\n# --runtime-only\n"},
		{name: "bundle/bin/helper", mode: 0o755, body: "#!/bin/sh\nprintf '%s\\n' synthetic-ok\n"},
		{name: "bundle/share/data.txt", mode: 0o644, body: "synthetic data\n"},
		{name: "bundle/share/private.txt", mode: 0o600, body: "synthetic private data\n"},
		{name: "bundle/bin/tool", mode: 0o750, body: "synthetic executable data\n"},
		{name: "bundle/bin/data", mode: 0o777, kind: tar.TypeSymlink, link: "../share/data.txt"},
		{name: "bundle/bin/data-alias", mode: 0o777, kind: tar.TypeSymlink, link: "data"},
		{name: "bundle/bin/share-dir", mode: 0o777, kind: tar.TypeSymlink, link: "../share"},
		{name: "bundle/bin/shared-data", mode: 0o777, kind: tar.TypeSymlink, link: "share-dir/data.txt"},
		{name: "bundle/tools/node", mode: 0o777, kind: tar.TypeSymlink, link: "../future/node"},
	}
	var manifest []lcuManifestEntry
	for _, f := range files {
		e := lcuManifestEntry{Path: f.name[len("bundle/"):]}
		if f.kind == tar.TypeSymlink {
			e.Link = f.link
		} else {
			sum := sha256.Sum256([]byte(f.body))
			e.Mode, e.SHA256 = uint32(f.mode), hex.EncodeToString(sum[:])
		}
		manifest = append(manifest, e)
	}
	b, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, lcuTarEntry{name: "bundle/manifest.json", body: string(b), mode: 0o644})
	return lcuTestArchive(t, files)
}

func TestLCUArchiveRejectsUnsafeEntries(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("LCU installation is supported on macOS and Linux")
	}
	script := lcuTarEntry{name: "bundle/scripts/install.sh", mode: 0o755, body: "#!/bin/sh\n# --runtime-only\n"}
	link := func(name, target string) lcuTarEntry {
		return lcuTarEntry{name: name, kind: tar.TypeSymlink, mode: 0o777, link: target}
	}
	file := func(name string) lcuTarEntry { return lcuTarEntry{name: name, mode: 0o644, body: "payload"} }
	cases := []struct {
		name    string
		entries []lcuTarEntry
	}{
		{"parent traversal", []lcuTarEntry{file("../sentinel")}},
		{"embedded traversal", []lcuTarEntry{file("bundle/../sentinel")}},
		{"absolute entry", []lcuTarEntry{file("/absolute")}},
		{"absolute link", []lcuTarEntry{link("bundle/absolute", "/absolute")}},
		{"escaping link", []lcuTarEntry{link("bundle/escape", "../../sentinel")}},
		{"empty link", []lcuTarEntry{link("bundle/empty", "")}},
		{"backslash link", []lcuTarEntry{link("bundle/backslash", `..\sentinel`)}},
		{"link chain escaping after resolution", []lcuTarEntry{link("bundle/a", "."), link("bundle/b", "a/../../sentinel")}},
		{"link cycle", []lcuTarEntry{link("bundle/a", "b"), link("bundle/b", "a")}},
		{"link then child", []lcuTarEntry{link("bundle/bridge", "share"), file("bundle/bridge/child")}},
		{"child then link", []lcuTarEntry{file("bundle/bridge/child"), link("bundle/bridge", "share")}},
		{"file then link", []lcuTarEntry{file("bundle/same"), link("bundle/same", "share")}},
		{"link then file", []lcuTarEntry{link("bundle/same", "share"), file("bundle/same")}},
		{"duplicate normalized file", []lcuTarEntry{file("bundle/same"), file("bundle/./same")}},
		{"directory then file", []lcuTarEntry{{name: "bundle/same", mode: 0o755, kind: tar.TypeDir}, file("bundle/same")}},
		{"hard link", []lcuTarEntry{{name: "bundle/hard", kind: tar.TypeLink, mode: 0o644, link: "../sentinel"}}},
		{"character device", []lcuTarEntry{{name: "bundle/device", kind: tar.TypeChar, mode: 0o600}}},
		{"block device", []lcuTarEntry{{name: "bundle/device", kind: tar.TypeBlock, mode: 0o600}}},
		{"fifo", []lcuTarEntry{{name: "bundle/fifo", kind: tar.TypeFifo, mode: 0o600}}},
		{"setuid file", []lcuTarEntry{{name: "bundle/privileged", mode: 0o4755, body: "payload"}}},
		{"setgid file", []lcuTarEntry{{name: "bundle/privileged", mode: 0o2755, body: "payload"}}},
		{"sticky directory", []lcuTarEntry{{name: "bundle/privileged", kind: tar.TypeDir, mode: 0o1755}}},
		{"group writable file", []lcuTarEntry{{name: "bundle/writable", mode: 0o666, body: "payload"}}},
		{"world writable executable", []lcuTarEntry{{name: "bundle/writable", mode: 0o777, body: "payload"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parent := t.TempDir()
			staging := filepath.Join(parent, "stage")
			if err := os.Mkdir(staging, 0o700); err != nil {
				t.Fatal(err)
			}
			sentinel := filepath.Join(parent, "sentinel")
			if err := os.WriteFile(sentinel, []byte("unchanged"), 0o600); err != nil {
				t.Fatal(err)
			}
			entries := append([]lcuTarEntry{script}, tc.entries...)
			if _, err := extractLCU(staging, lcuTestArchive(t, entries)); err == nil {
				t.Error("unsafe archive was accepted")
			}
			data, err := os.ReadFile(sentinel)
			if err != nil || string(data) != "unchanged" {
				t.Errorf("outside sentinel: %q, %v", data, err)
			}
		})
	}
}

func TestLCUArchiveRequiresEmptyPrivateStaging(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("LCU installation is supported on macOS and Linux")
	}
	archive := lcuLayoutArchive(t)
	t.Run("nonempty", func(t *testing.T) {
		dir := lcuTestStage(t)
		if err := os.WriteFile(filepath.Join(dir, "existing"), []byte("original"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := extractLCU(dir, archive); err == nil {
			t.Error("nonempty staging directory was accepted")
		}
		data, err := os.ReadFile(filepath.Join(dir, "existing"))
		if err != nil || string(data) != "original" {
			t.Errorf("existing file: %q, %v", data, err)
		}
	})
	t.Run("not private", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := extractLCU(dir, archive); err == nil {
			t.Error("public staging directory was accepted")
		}
	})
	t.Run("symlink root", func(t *testing.T) {
		dir := lcuTestStage(t)
		alias := filepath.Join(t.TempDir(), "stage")
		if err := os.Symlink(dir, alias); err != nil {
			t.Fatal(err)
		}
		if _, err := extractLCU(alias, archive); err == nil {
			t.Error("symlink staging root was accepted")
		}
	})
}

func TestLCUArchiveRejectsCaseAliasedLinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("LCU installation is supported on macOS and Linux")
	}
	parent := t.TempDir()
	probe := filepath.Join(parent, "CaseProbe")
	if err := os.WriteFile(probe, []byte("probe"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(parent, "caseprobe")); errors.Is(err, os.ErrNotExist) {
		t.Skip("the staging filesystem is case-sensitive")
	} else if err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(parent, "stage")
	if err := os.Mkdir(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	archive := lcuTestArchive(t, []lcuTarEntry{
		{name: "bundle/scripts/install.sh", mode: 0o755, body: "#!/bin/sh\n# --runtime-only\n"},
		{name: "bundle/A", kind: tar.TypeSymlink, mode: 0o777, link: "."},
		{name: "bundle/b", kind: tar.TypeSymlink, mode: 0o777, link: "missing/../a/../../outside"},
	})
	if _, err := extractLCU(stage, archive); err == nil {
		t.Fatal("case-aliased link chain escaped the staging directory")
	}
}

func readLCUSyntheticManifest(t *testing.T, root string) []lcuManifestEntry {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest []lcuManifestEntry
	if err := json.Unmarshal(b, &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func lcuManifestEntryError(root string, entry lcuManifestEntry) error {
	name := filepath.Join(root, filepath.FromSlash(entry.Path))
	info, err := os.Lstat(name)
	if err != nil {
		return err
	}
	if entry.Link != "" {
		if info.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("expected a symlink, got %v", info.Mode())
		}
		if target, err := os.Readlink(name); err != nil || target != entry.Link {
			return fmt.Errorf("target = %q, err = %v; want %q", target, err, entry.Link)
		}
		return nil
	}
	if !info.Mode().IsRegular() || uint32(info.Mode().Perm()) != entry.Mode {
		return fmt.Errorf("mode = %v; want regular %04o", info.Mode(), entry.Mode)
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != entry.SHA256 {
		return errors.New("file digest differs from the synthetic manifest")
	}
	return nil
}

func checkLCUSyntheticManifest(t *testing.T, root string) {
	t.Helper()
	manifest := readLCUSyntheticManifest(t, root)
	for _, entry := range manifest {
		t.Run(entry.Path, func(t *testing.T) {
			if err := lcuManifestEntryError(root, entry); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestLCUArchiveManifestDetectsChanges(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("LCU installation is supported on macOS and Linux")
	}
	for _, tc := range []struct {
		name, entry string
		mutate      func(string) error
	}{
		{"mode", "bin/helper", func(name string) error { return os.Chmod(name, 0o644) }},
		{"content", "share/data.txt", func(name string) error { return os.WriteFile(name, []byte("changed"), 0o644) }},
		{"link target", "bin/data", func(name string) error {
			if err := os.Remove(name); err != nil {
				return err
			}
			return os.Symlink("../share/private.txt", name)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, err := extractLCU(lcuTestStage(t), lcuLayoutArchive(t))
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range readLCUSyntheticManifest(t, root) {
				if entry.Path != tc.entry {
					continue
				}
				if err := lcuManifestEntryError(root, entry); err != nil {
					t.Fatal(err)
				}
				if err := tc.mutate(filepath.Join(root, filepath.FromSlash(entry.Path))); err != nil {
					t.Fatal(err)
				}
				if err := lcuManifestEntryError(root, entry); err == nil {
					t.Fatal("manifest verification accepted a changed entry")
				}
				return
			}
			t.Fatal("entry absent from synthetic manifest")
		})
	}
}

func TestLCUArchivePreservesManifestLayout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("LCU installation is supported on macOS and Linux")
	}
	root, err := extractLCU(lcuTestStage(t), lcuLayoutArchive(t))
	if err != nil {
		t.Fatal(err)
	}
	checkLCUSyntheticManifest(t, root)
	for _, name := range []string{"data-alias", "shared-data"} {
		t.Run("resolve "+name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(root, "bin", name))
			if err != nil || string(data) != "synthetic data\n" {
				t.Errorf("resolved data = %q, %v", data, err)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(root, "tools", "node")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected an initially dangling link: %v", err)
	}
	t.Run("direct synthetic helper", func(t *testing.T) {
		out, err := exec.Command(filepath.Join(root, "bin", "helper")).CombinedOutput()
		if err != nil || string(out) != "synthetic-ok\n" {
			t.Errorf("synthetic helper: %q, %v", out, err)
		}
	})
}

func TestLCUArchiveLayoutUnderUmask(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("LCU installation is supported on macOS and Linux")
	}
	if os.Getenv("PERSONA_TEST_LCU_UMASK") == "child" {
		root, err := extractLCU(lcuTestStage(t), lcuLayoutArchive(t))
		if err != nil {
			t.Fatal(err)
		}
		checkLCUSyntheticManifest(t, root)
		return
	}
	for _, mask := range []string{"022", "077"} {
		t.Run(mask, func(t *testing.T) {
			cmd := exec.Command("/bin/sh", "-c", `umask "$1"; exec "$2" -test.run '^TestLCUArchiveLayoutUnderUmask$' -test.v`, "lcu-test", mask, os.Args[0])
			cmd.Env = append(os.Environ(), "PERSONA_TEST_LCU_UMASK=child")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("umask %s: %v\n%s", mask, err, out)
			}
		})
	}
}
