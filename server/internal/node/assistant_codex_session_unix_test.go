//go:build !windows

package node

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/identity"
	"golang.org/x/sys/unix"
)

func TestCodexAssistantOwnSessionAndGroupCleanup(t *testing.T) {
	fakeCodexHome(t, "019a1930-7e92-7073-9bb8-d2ad3126f329")
	r := newSpaceRig(t)
	cwd := filepath.Join(r.state, identity.SpaceHealth)
	r.n.mu.Lock()
	r.n.modes, r.n.akeys, r.n.metas, r.n.bgScans = map[string]sessionModes{}, map[string]*assistantProc{}, map[string]quickMeta{}, map[string]*bgScan{}
	r.n.mu.Unlock()
	p, err := r.n.spawnCodex("", cwd, "codex", false, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.stop)
	pid := p.cmd.Process.Pid
	if sid, err := unix.Getsid(pid); err != nil || sid != pid {
		t.Fatalf("session = %d, %v; want child session %d without the Hub's controlling terminal", sid, err, pid)
	}
	if pgid, err := unix.Getpgid(pid); err != nil || pgid != pid {
		t.Fatalf("process group = %d, %v; want child group %d", pgid, err, pid)
	}
	p.stop()
	select {
	case <-p.exited:
	case <-time.After(5 * time.Second):
		t.Fatal("stopping the assistant did not reap its process")
	}
}
