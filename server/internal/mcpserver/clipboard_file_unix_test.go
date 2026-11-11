//go:build darwin || linux

package mcpserver

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A named pipe is refused without waiting for a writer.
func TestClipboardFileRefusesANamedPipe(t *testing.T) {
	home, _ := clipboardHome(t)
	fifo := filepath.Join(home, "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skip("no mkfifo:", err)
	}
	sent := phoneNode(t, "")
	done := make(chan struct{})
	var text string
	var isErr bool
	go func() { defer close(done); text, isErr = clipboardCall(t, map[string]any{"file": fifo}) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("waited on the named pipe")
	}
	if !isErr || !strings.Contains(text, "is a named pipe") || len(*sent) != 0 {
		t.Fatalf("named pipe: %q", text)
	}
}

// The path's last part swapped for a symlink after it was resolved is not followed.
func TestOpenClipboardFileDoesNotFollowASymlink(t *testing.T) {
	dir := t.TempDir()
	target := writeFile(t, filepath.Join(dir, "target"), []byte(testSecret))
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if f, err := openClipboardFile(link); err == nil {
		f.Close()
		t.Fatal("opened through a symlink")
	}
}

type ownerInfo struct {
	fs.FileInfo
	uid uint32
}

func (o ownerInfo) Sys() any { return &syscall.Stat_t{Uid: o.uid} }

// Only a file of the user this process runs as is copied (another user's cannot be made without root here).
func TestOwnedByCaller(t *testing.T) {
	st, err := os.Stat(writeFile(t, filepath.Join(t.TempDir(), "mine"), []byte("x")))
	if err != nil {
		t.Fatal(err)
	}
	if !ownedByCaller(st) {
		t.Fatal("my own file is not mine")
	}
	if ownedByCaller(ownerInfo{st, uint32(os.Geteuid() + 1)}) {
		t.Fatal("another user's file passed")
	}
}
