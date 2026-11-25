package extension

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A snapshot of an extension's data has its files, directories and links; at most BackupsKept stay, the newest; it
// puts the directory back whole, dropping what came after it.
func TestBackupDataKeepsThreeAndRestores(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, nil, 1)
	at := time.Date(2026, 11, 24, 18, 24, 0, 0, time.UTC)
	r.now = func() time.Time { at = at.Add(time.Second); return at }
	data := r.DataDir("example")

	// No data directory: nothing to keep, no error, no file.
	if path, _, err := r.BackupData("example", "1.0.0"); err != nil || path != "" {
		t.Fatalf("without data: %q %v", path, err)
	}
	if got := r.Backups("example"); len(got) != 0 {
		t.Fatalf("backups without data: %v", got)
	}

	if err := os.MkdirAll(filepath.Join(data, "db", "empty"), 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(data, "db", "memory.sqlite"), []byte("v1 rows"), 0o600)
	_ = os.WriteFile(filepath.Join(data, "state.json"), []byte(`{"schema":1}`), 0o600)
	_ = os.Symlink("db/memory.sqlite", filepath.Join(data, "current"))
	first, files, err := r.BackupData("example", "1.0.0")
	if err != nil || first == "" || files != 2 {
		t.Fatalf("first backup: %q %d %v", first, files, err)
	}
	if filepath.Dir(first) != r.BackupDir("example") || !strings.HasSuffix(first, "-1.0.0.tar.gz") {
		t.Fatalf("where: %s", first)
	}

	// Four more: only the newest three are kept, the first one among those gone.
	var last string
	for i := range 4 {
		_ = os.WriteFile(filepath.Join(data, "state.json"), []byte(`{"schema":`+string(rune('2'+i))+`}`), 0o600)
		if last, _, err = r.BackupData("example", "1.0.0"); err != nil {
			t.Fatal(err)
		}
	}
	kept := r.Backups("example")
	if len(kept) != BackupsKept || kept[0] != last {
		t.Fatalf("kept: %v (newest %s)", kept, last)
	}
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Fatalf("the oldest is still there: %v", err)
	}
	entries, _ := os.ReadDir(r.BackupDir("example"))
	if len(entries) != BackupsKept {
		t.Fatalf("left in the backup directory: %v", entries)
	}

	// The newer version migrates: changes a file, adds one, removes one.
	_ = os.WriteFile(filepath.Join(data, "db", "memory.sqlite"), []byte("v2 rows"), 0o600)
	_ = os.WriteFile(filepath.Join(data, "migrated"), []byte("x"), 0o600)
	_ = os.Remove(filepath.Join(data, "state.json"))
	if err := r.RestoreData("example", last); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(data, "db", "memory.sqlite")); string(b) != "v1 rows" {
		t.Fatalf("memory.sqlite: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(data, "state.json")); string(b) != `{"schema":5}` {
		t.Fatalf("state.json: %q", b)
	}
	if _, err := os.Stat(filepath.Join(data, "migrated")); !os.IsNotExist(err) {
		t.Fatalf("what the newer version wrote stayed: %v", err)
	}
	if to, err := os.Readlink(filepath.Join(data, "current")); err != nil || to != "db/memory.sqlite" {
		t.Fatalf("link: %q %v", to, err)
	}
	if fi, err := os.Stat(filepath.Join(data, "db", "empty")); err != nil || !fi.IsDir() {
		t.Fatalf("empty directory: %v", err)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(data), ".restore-*")); len(left) != 0 {
		t.Fatalf("restore left %v", left)
	}

	// A snapshot taken without a data directory removes the one the newer version made.
	if err := r.RestoreData("example", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(data); !os.IsNotExist(err) {
		t.Fatalf("data directory stayed: %v", err)
	}
}

// An archive whose names leave the data directory, or that is not one of the extension's, is refused and leaves the
// directory as it was.
func TestRestoreDataRefusesForeignArchives(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, nil, 1)
	data := r.DataDir("example")
	_ = os.MkdirAll(data, 0o700)
	_ = os.WriteFile(filepath.Join(data, "keep"), []byte("as is"), 0o600)
	_ = os.MkdirAll(r.BackupDir("example"), 0o700)
	bad := filepath.Join(r.BackupDir("example"), "20261124T182400.000000000Z-1.0.0.tar.gz")
	f, err := os.Create(bad)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "../escaped", Mode: 0o600, Size: 1, Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte("x"))
	_ = tw.Close()
	_ = gz.Close()
	_ = f.Close()
	if err := r.RestoreData("example", bad); err == nil {
		t.Fatal("an entry leaving the directory was restored")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(data), "escaped")); !os.IsNotExist(err) {
		t.Fatalf("written outside: %v", err)
	}
	if err := r.RestoreData("example", filepath.Join(t.TempDir(), "other.tar.gz")); err == nil {
		t.Fatal("an archive from elsewhere was restored")
	}
	if b, _ := os.ReadFile(filepath.Join(data, "keep")); string(b) != "as is" {
		t.Fatalf("data changed: %q", b)
	}
}

// The manifest's upgrade object: backup auto or required, prepare and health_check only with a program that speaks
// the protocol, nothing else in it.
func TestManifestUpgradeSpec(t *testing.T) {
	m, err := Parse(with(t, func(m map[string]any) {
		m["upgrade"] = map[string]any{"backup": "required", "prepare": true, "health_check": true}
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !m.BackupRequired() || !m.UpgradePrepare() || !m.UpgradeHealthCheck() {
		t.Fatalf("%+v", m.Upgrade)
	}
	if m, err := Parse([]byte(exampleManifest)); err != nil || m.BackupRequired() || m.UpgradePrepare() || m.UpgradeHealthCheck() {
		t.Fatalf("without upgrade: %v", err)
	}
	for says, change := range map[string]func(m map[string]any){
		"upgrade.backup": func(m map[string]any) { m["upgrade"] = map[string]any{"backup": "never"} },
		"hook":           func(m map[string]any) { m["upgrade"] = map[string]any{"hook": "x"} },
		"upgrade.prepare and upgrade.health_check": func(m map[string]any) {
			m["runtime"].(map[string]any)["rpc"] = "none"
			m["runtime"].(map[string]any)["http"] = true
			m["activation_events"] = []any{"on_startup"}
			m["upgrade"] = map[string]any{"health_check": true}
		},
	} {
		if _, err := Parse(with(t, change)); err == nil || !strings.Contains(err.Error(), says) {
			t.Errorf("refused for %s? %v", says, err)
		}
	}
}
