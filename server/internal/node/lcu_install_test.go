package node

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestLCUVersionFromTheReleaseDirectory(t *testing.T) {
	dir := t.TempDir()
	release := filepath.Join(dir, "lcu-0.13.5-darwin-arm64")
	if err := os.MkdirAll(filepath.Join(release, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(release, "bin", "lcu"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(dir, "current")
	if err := os.Symlink(release, current); err != nil {
		t.Fatal(err)
	}
	if got := lcuVersion(filepath.Join(current, "bin", "lcu")); got != "0.13.5" {
		t.Fatal(got)
	}
}

func TestLCUReadySaysWhatIsMissing(t *testing.T) {
	oldOS, oldArch := installOS, installArch
	installOS, installArch = "darwin", "arm64"
	t.Cleanup(func() { installOS, installArch = oldOS, oldArch })
	if ok, note := lcuReady(proto.Computer{}); ok || !strings.Contains(note, "没有装 LCU") {
		t.Fatalf("%v %s", ok, note)
	}
	if ok, note := lcuReady(proto.Computer{Installed: true}); ok || !strings.Contains(note, "ChatGPT") {
		t.Fatalf("%v %s", ok, note)
	}
	if ok, note := lcuReady(proto.Computer{Installed: true, ChatGPT: true, Desktop: "active"}); !ok || note != "" {
		t.Fatalf("ready: %v %s", ok, note)
	}
	installArch = "amd64"
	if ok, note := lcuReady(proto.Computer{Installed: true, ChatGPT: true}); ok || !strings.Contains(note, "Apple") {
		t.Fatalf("%v %s", ok, note)
	}
}

func TestLCUInstallVerifiesTheArchiveAndDoesNotRegisterAHarness(t *testing.T) {
	stagingParent := t.TempDir()
	t.Setenv("TMPDIR", stagingParent)
	script := []byte("#!/bin/sh\n# --runtime-only is the install mode that skips harness registration\n")
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := script
	hdr := &tar.Header{Name: "lcu-1.2.3-darwin-arm64/scripts/install.sh", Mode: 0o755, Size: int64(len(body))}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil || gz.Close() != nil {
		t.Fatal("tar")
	}
	archive := buf.Bytes()
	sum := sha256.Sum256(archive)
	sidecar := hex.EncodeToString(sum[:]) + "  lcu-1.2.3-darwin-arm64.tar.gz\n"
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/amontlabs/lcu/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"assets": []map[string]string{
			{"name": "lcu-1.2.3-darwin-arm64.tar.gz", "browser_download_url": "http://" + r.Host + "/archive"},
			{"name": "lcu-1.2.3-darwin-arm64.tar.gz.sha256", "browser_download_url": "http://" + r.Host + "/sha"},
		}})
	})
	mux.HandleFunc("/archive", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(archive) })
	mux.HandleFunc("/sha", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(sidecar)) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	oldAPI, oldBin, oldApp, oldOS, oldArch, oldRun := lcuReleaseAPI, lcuBin, chatgptApp, installOS, installArch, lcuInstallRun
	lcuReleaseAPI = srv.URL + "/repos/amontlabs/lcu/releases/latest"
	lcuBin = func() string { return "" }
	chatgptApp = func() bool { return true }
	installOS, installArch = "darwin", "arm64"
	var root string
	runs := 0
	lcuInstallRun = func(ctx context.Context, dir string) error {
		runs++
		root = dir
		if b, err := os.ReadFile(filepath.Join(dir, "scripts", "install.sh")); err != nil || !bytes.Contains(b, []byte("--runtime-only")) {
			t.Errorf("extracted installer: %v %s", err, b)
		}
		args := lcuInstallArgs(dir)
		if strings.Join(args, " ") != "/bin/bash "+filepath.Join(dir, "scripts", "install.sh")+" --runtime-only --yes" {
			t.Errorf("args: %q", args)
		}
		for _, a := range args {
			if a == "--agent" || strings.Contains(a, "claude") || strings.Contains(a, "codex") {
				t.Errorf("harness registration: %q", args)
			}
		}
		return nil
	}
	t.Cleanup(func() {
		lcuReleaseAPI, lcuBin, chatgptApp, installOS, installArch, lcuInstallRun = oldAPI, oldBin, oldApp, oldOS, oldArch, oldRun
	})
	if err := installLCU(context.Background(), func(string) {}); err != nil {
		t.Fatal(err)
	}
	if root == "" {
		t.Fatal("installer did not run")
	}
	if _, err := os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("successful install did not clean staging: %v", err)
	}
	checkClean := func() {
		t.Helper()
		entries, err := os.ReadDir(stagingParent)
		if err != nil || len(entries) != 0 {
			t.Fatalf("staging after install: %v, %v", entries, err)
		}
		if runs != 1 {
			t.Fatalf("installer ran for invalid archive: %d calls", runs)
		}
	}
	checkClean()

	sidecar = strings.Repeat("0", 64)
	if err := installLCU(context.Background(), func(string) {}); err == nil {
		t.Fatal("accepted an invalid checksum")
	}
	checkClean()

	archive = lcuTestArchive(t, []lcuTarEntry{
		{name: "bundle/scripts/install.sh", mode: 0o755, body: string(script)},
		{name: "../escape", mode: 0o644, body: "payload"},
	})
	sum = sha256.Sum256(archive)
	sidecar = hex.EncodeToString(sum[:])
	if err := installLCU(context.Background(), func(string) {}); err == nil {
		t.Fatal("accepted an unsafe archive")
	}
	checkClean()

	archive = lcuTestArchive(t, []lcuTarEntry{{name: "bundle/scripts/install.sh", mode: 0o755, body: "not a script --runtime-only"}})
	sum = sha256.Sum256(archive)
	sidecar = hex.EncodeToString(sum[:])
	if err := installLCU(context.Background(), func(string) {}); err == nil {
		t.Fatal("accepted an invalid installer")
	}
	checkClean()

	chatgptApp = func() bool { return false }
	if err := installLCU(context.Background(), func(string) {}); err == nil || !strings.Contains(err.Error(), "ChatGPT") {
		t.Fatalf("missing app: %v", err)
	}
}

func TestLCUArchiveChecksumAndTraversal(t *testing.T) {
	if err := checkSHA256([]byte("hello"), []byte("nope  file\n")); err == nil {
		t.Fatal("bad checksum")
	}
	sum := sha256.Sum256([]byte("hello"))
	if err := checkSHA256([]byte("hello"), []byte(hex.EncodeToString(sum[:])+"  file\n")); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	payload := []byte("x")
	_ = tw.WriteHeader(&tar.Header{Name: "../evil", Mode: 0o644, Size: 1})
	_, _ = tw.Write(payload)
	_ = tw.Close()
	_ = gz.Close()
	if _, err := extractLCU(lcuTestStage(t), buf.Bytes()); err == nil || !strings.Contains(err.Error(), "不安全") {
		t.Fatalf("%v", err)
	}
}

func TestLCUInstallForwardingWrapper(t *testing.T) {
	// The wrapper and target are synthetic; neither is executed. The approved wrapper hash is replaced only in this test.
	wrapper := "#!/bin/sh\nexec \"$node\" \"$source_dir/scripts/install.mjs\" \"$@\"\n"
	installer := `const OPTIONS = { 'runtime-only': { type: 'boolean', default: false } };
export async function main(argv) {
  let values;
  try { values = parse(argv, OPTIONS); } catch (error) { return 2; }
  const runtimeOnly = values['runtime-only'];
  if (runtimeOnly) { return 0; }
  const forwarded = setupArguments(values);
  return forwarded;
}
if (isMain(import.meta)) run('LCU installer', () => main(process.argv.slice(2)));
`
	digest := func(text string) string {
		sum := sha256.Sum256([]byte(text))
		return hex.EncodeToString(sum[:])
	}
	oldHashes := lcuForwardingWrapperHashes
	lcuForwardingWrapperHashes = []string{digest(wrapper)}
	t.Cleanup(func() { lcuForwardingWrapperHashes = oldHashes })

	writePair := func(t *testing.T, shell, target string) (string, string) {
		t.Helper()
		dir := t.TempDir()
		script := filepath.Join(dir, "install.sh")
		module := filepath.Join(dir, "install.mjs")
		if err := os.WriteFile(script, []byte(shell), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(module, []byte(target), 0o644); err != nil {
			t.Fatal(err)
		}
		return script, module
	}
	t.Run("approved wrapper and target", func(t *testing.T) {
		script, _ := writePair(t, wrapper, installer)
		if err := checkLCUInstaller(script); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("compatible target changes", func(t *testing.T) {
		updated := strings.Replace(installer, "{ 'runtime-only'", "{ another: { type: 'boolean' }, 'runtime-only'", 1)
		updated = strings.Replace(updated, "try { values =", "try { note(); values =", 1)
		updated = strings.ReplaceAll(updated, "forwarded", "setupArgs")
		script, _ := writePair(t, wrapper, updated+"// Unrelated synthetic change.\nconst docs = 'https://example.com/';\n")
		if err := checkLCUInstaller(script); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("legacy flag promotion", func(t *testing.T) {
		updated := strings.Replace(installer, "const runtimeOnly", "const legacy = false;\n  if (legacy && !values.agent.length && !values.export) values['runtime-only'] = true;\n  const runtimeOnly", 1)
		script, _ := writePair(t, wrapper, updated)
		if err := checkLCUInstaller(script); err != nil {
			t.Fatal(err)
		}
	})
	for _, tc := range []struct{ name, shell, target string }{
		{"omitted arguments", strings.Replace(wrapper, ` "$@"`, "", 1), installer},
		{"dropped arguments", strings.Replace(wrapper, `"$@"`, `"$1"`, 1), installer},
		{"wrong target", strings.Replace(wrapper, "install.mjs", "other.mjs", 1), installer},
		{"modified wrapper", strings.Replace(wrapper, "exec ", "echo ignored\nexec ", 1), installer},
		{"unsupported option", wrapper, strings.Replace(installer, "'runtime-only':", "'other':", 1)},
		{"target does not parse argv", wrapper, strings.Replace(installer, "parse(argv, OPTIONS)", "parse([], OPTIONS)", 1)},
		{"target does not read option", wrapper, strings.Replace(installer, "values['runtime-only']", "false", 1)},
		{"target has no runtime branch", wrapper, strings.Replace(installer, "if (runtimeOnly)", "if (false)", 1)},
		{"target does not return early", wrapper, strings.Replace(installer, "return 0;", "doSetup();", 1)},
		{"target return is conditional", wrapper, strings.Replace(installer, "if (runtimeOnly) { return 0; }", "if (runtimeOnly) { if (false) return 0; }", 1)},
		{"target return is nested", wrapper, strings.Replace(installer, "if (runtimeOnly) { return 0; }", "if (runtimeOnly) { if (false) { return 0; } }", 1)},
		{"target discards parsed values", wrapper, strings.Replace(installer, "values = parse(argv, OPTIONS);", "parse(argv, OPTIONS);", 1)},
		{"target conditionally parses", wrapper, strings.Replace(installer, "try {", "if (false) try {", 1)},
		{"target overwrites parsed values", wrapper, strings.Replace(installer, "const runtimeOnly", "values = {}; const runtimeOnly", 1)},
		{"target disables runtime-only", wrapper, strings.Replace(installer, "const runtimeOnly", "values['runtime-only'] = false; const runtimeOnly", 1)},
		{"target overwrites escaped key", wrapper, strings.Replace(installer, "const runtimeOnly", `values['runtime\x2donly'] = false; const runtimeOnly`, 1)},
		{"target overwrites computed key", wrapper, strings.Replace(installer, "const runtimeOnly", `values['runtime' + '-only'] = false; const runtimeOnly`, 1)},
		{"target deletes flag", wrapper, strings.Replace(installer, "const runtimeOnly", "delete values['runtime-only']; const runtimeOnly", 1)},
		{"target reassigns bound flag", wrapper, strings.Replace(installer, "if (runtimeOnly)", "runtimeOnly = false; if (runtimeOnly)", 1)},
		{"target duplicates option", wrapper, strings.Replace(installer, "default: false } };", "default: false }, 'runtime-only': { type: 'boolean' } };", 1)},
		{"target uses an unbound flag", wrapper, strings.Replace(installer, "const runtimeOnly = values['runtime-only'];", "values['runtime-only']; const runtimeOnly = false;", 1)},
		{"target markers in a comment", wrapper, "/*\n" + installer + "\n*/\n"},
		{"target markers in a string", wrapper, "const decoy = " + strconv.Quote(installer) + ";\n"},
		{"target markers in a template", wrapper, "const decoy = `" + installer + "`;\n"},
		{"target uses division", wrapper, installer + "\nconst ratio = 4 / 2;\n"},
		{"target uses a regex literal", wrapper, installer + "\nconst pattern = /example/;\n"},
		{"target exceeds source limit", wrapper, installer + strings.Repeat(" ", lcuInstallerSourceMost)},
		{"target does not forward process arguments", wrapper, strings.Replace(installer, "main(process.argv.slice(2))", "main([])", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script, _ := writePair(t, tc.shell, tc.target)
			if err := checkLCUInstaller(script); err == nil || !strings.Contains(err.Error(), "update Persona") {
				t.Errorf("unapproved forwarding pair did not give an actionable rejection: %v", err)
			}
		})
	}
	for _, tc := range []struct {
		name   string
		change func(t *testing.T, target string)
	}{
		{"missing target", func(t *testing.T, target string) {
			if err := os.Remove(target); err != nil {
				t.Fatal(err)
			}
		}},
		{"symlink target", func(t *testing.T, target string) {
			if err := os.Remove(target); err != nil {
				t.Fatal(err)
			}
			other := filepath.Join(t.TempDir(), "module")
			if err := os.WriteFile(other, []byte(installer), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(other, target); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script, target := writePair(t, wrapper, installer)
			tc.change(t, target)
			if err := checkLCUInstaller(script); err == nil || !strings.Contains(err.Error(), "update Persona") {
				t.Errorf("invalid forwarding target did not give an actionable rejection: %v", err)
			}
		})
	}
	t.Run("legacy direct script", func(t *testing.T) {
		script, target := writePair(t, "#!/bin/sh\n# --runtime-only\n", "")
		if err := os.Remove(target); err != nil {
			t.Fatal(err)
		}
		if err := checkLCUInstaller(script); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("actual install entry", func(t *testing.T) {
		archive := lcuTestArchive(t, []lcuTarEntry{
			{name: "bundle/scripts/install.sh", mode: 0o755, body: wrapper},
			{name: "bundle/scripts/install.mjs", mode: 0o644, body: installer + "// Unrelated synthetic change.\n"},
		})
		sum := sha256.Sum256(archive)
		mux := http.NewServeMux()
		mux.HandleFunc("/release", func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"assets": []map[string]string{
				{"name": "lcu-1.2.3-darwin-arm64.tar.gz", "browser_download_url": "http://" + r.Host + "/archive"},
				{"name": "lcu-1.2.3-darwin-arm64.tar.gz.sha256", "browser_download_url": "http://" + r.Host + "/sum"},
			}})
		})
		mux.HandleFunc("/archive", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(archive) })
		mux.HandleFunc("/sum", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(hex.EncodeToString(sum[:]))) })
		server := httptest.NewServer(mux)
		t.Cleanup(server.Close)
		oldAPI, oldBin, oldApp, oldOS, oldArch, oldRun := lcuReleaseAPI, lcuBin, chatgptApp, installOS, installArch, lcuInstallRun
		t.Cleanup(func() {
			lcuReleaseAPI, lcuBin, chatgptApp, installOS, installArch, lcuInstallRun = oldAPI, oldBin, oldApp, oldOS, oldArch, oldRun
		})
		lcuReleaseAPI = server.URL + "/release"
		lcuBin = func() string { return "" }
		chatgptApp = func() bool { return true }
		installOS, installArch = "darwin", "arm64"
		var staged string
		lcuInstallRun = func(_ context.Context, root string) error {
			staged = root
			args := lcuInstallArgs(root)
			if len(args) != 4 || args[0] != "/bin/bash" || args[1] != filepath.Join(root, "scripts", "install.sh") || args[2] != "--runtime-only" || args[3] != "--yes" {
				t.Errorf("unexpected installer arguments: %q", args)
			}
			return nil
		}
		if err := installLCU(context.Background(), func(string) {}); err != nil {
			t.Fatal(err)
		}
		if staged == "" {
			t.Fatal("stubbed installer was not reached")
		}
		if _, err := os.Lstat(staged); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("staging directory was not removed: %v", err)
		}
	})
}
