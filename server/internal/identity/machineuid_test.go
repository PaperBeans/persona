package identity

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadMachineUID(t *testing.T) {
	dir := t.TempDir()
	// Missing file returns "" and must not create machine-uid.
	if got := ReadMachineUID(dir); got != "" {
		t.Fatalf("missing file: got %q, want empty", got)
	}
	if _, err := os.Stat(filepath.Join(dir, machineUIDFile)); !os.IsNotExist(err) {
		t.Fatalf("ReadMachineUID created %s: %v", machineUIDFile, err)
	}
	if got := ReadMachineUID(""); got != "" {
		t.Fatalf("empty dir: got %q, want empty", got)
	}

	// Invalid contents return "".
	path := filepath.Join(dir, machineUIDFile)
	if err := os.WriteFile(path, []byte("not-a-uuid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ReadMachineUID(dir); got != "" {
		t.Fatalf("invalid content: got %q, want empty", got)
	}

	// Valid UUIDv4 is trimmed and returned.
	const want = "12345678-1234-4123-8123-123456789abc"
	if err := os.WriteFile(path, []byte("  "+want+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ReadMachineUID(dir); got != want {
		t.Fatalf("valid content: got %q, want %q", got, want)
	}
}
