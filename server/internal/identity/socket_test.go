package identity

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSocketDirectorySupportsTheUnixPathLimit(t *testing.T) {
	cleanRootEnv(t)
	limit := 103
	if runtime.GOOS == "linux" {
		limit = 107
	} else if runtime.GOOS != "darwin" {
		t.Skip("Unix socket path limit is platform-specific")
	}
	// Prefer the host's temporary directory. Deep TMPDIR values need a shorter fallback.
	var home string
	for _, root := range []string{os.TempDir(), "/tmp"} {
		base, err := os.MkdirTemp(root, "pc-")
		if err != nil {
			t.Logf("cannot create the socket fixture in %s: %v", root, err)
			continue
		}
		t.Cleanup(func() { os.RemoveAll(base) })
		count := limit - len(filepath.Join(base, ".persona", "node-attach.sock")) - 1
		if count > 0 {
			home = filepath.Join(base, strings.Repeat("h", count))
			break
		}
	}
	if home == "" {
		t.Skip("no writable temporary directory fits the Unix socket path limit")
	}
	if _, err := PrepareHome(home); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(SocketDir(home), "node-attach.sock")
	if len(path) != limit {
		t.Fatalf("Unix path length changed: %d, want %d", len(path), limit)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal("valid Unix socket path was rejected:", err)
	}
	listener.Close()
}
