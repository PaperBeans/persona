//go:build linux

package node

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// On Linux, agyPresenceHeld reads /proc/locks without calling flock: a real LOCK_EX holder is detected, a released lock
// or shared LOCK_SH is not, and an unreadable /proc/locks falls back cleanly to recent transcript writes (#14561).
func TestAgyPresenceHeldLinuxProcLocks(t *testing.T) {
	if _, err := os.ReadFile(agyProcLocksPath); err != nil {
		t.Skipf("/proc/locks unreadable in this environment: %v", err)
	}

	dir := t.TempDir()
	if agyPresenceHeld(dir, agyTestID) {
		t.Fatal("no lock file: held")
	}

	release := agyPresenceLockFile(t, dir, agyTestID)
	if !agyPresenceHeld(dir, agyTestID) {
		t.Fatal("a real LOCK_EX presence lock read as free via /proc/locks")
	}

	release()
	// Even with a fresh transcript, Linux /proc/locks definitively knows the lock is free.
	writeAgyTranscriptAt(t, dir, agyTestID, agyTranscriptPath, time.Now())
	if agyPresenceHeld(dir, agyTestID) {
		t.Fatal("released presence lock read as held via /proc/locks")
	}

	// A shared LOCK_SH lock is not an agy CLI exclusive presence lock.
	f, err := os.Open(filepath.Join(dir, "presence", agyTestID+".lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	if agyPresenceHeld(dir, agyTestID) {
		t.Fatal("shared LOCK_SH lock read as held")
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	// Unreadable /proc/locks falls back to transcript activity without calling flock.
	oldPath := agyProcLocksPath
	agyProcLocksPath = filepath.Join(dir, "nonexistent-proc-locks")
	t.Cleanup(func() { agyProcLocksPath = oldPath })

	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	withAgyPresenceHooks(t, nil, func() time.Time { return now })
	writeAgyTranscriptAt(t, dir, agyTestID, agyTranscriptPath, now.Add(-5*time.Second))
	if !agyPresenceHeld(dir, agyTestID) {
		t.Fatal("unreadable /proc/locks with recent transcript write read as free")
	}
	writeAgyTranscriptAt(t, dir, agyTestID, agyTranscriptPath, now.Add(-2*time.Minute))
	if agyPresenceHeld(dir, agyTestID) {
		t.Fatal("unreadable /proc/locks with stale transcript read as held")
	}
}

func TestAgyCompanionRowRefusedLinuxRealFlock(t *testing.T) {
	if _, err := os.ReadFile(agyProcLocksPath); err != nil {
		t.Skipf("/proc/locks unreadable in this environment: %v", err)
	}

	n, _, _ := herdrNode(t)
	dir := t.TempDir()
	t.Setenv("ANTIGRAVITY_CLI_CONFIG_DIR", dir)
	acpRow := "agy-" + agyTestID
	release := agyPresenceLockFile(t, dir, agyTestID)

	for _, method := range []string{"session.send", proto.MethodSessionSendUnseen, "session.interrupt", "session.rewind"} {
		_, err := request(t, n, method, map[string]any{"session_id": acpRow, "text": "x", "client_msg_id": "c1"})
		if refusalCode(err) != codeAgyInTerminal {
			t.Errorf("%s while a terminal holds the real flock: %v", method, err)
		}
	}
	if _, _, held := n.agyOpenCall("session.items", acpRow); held {
		t.Error("a read was refused")
	}
	release()
	if _, _, held := n.agyOpenCall("session.send", acpRow); held {
		t.Error("refused after the terminal's agy released the flock")
	}
}
