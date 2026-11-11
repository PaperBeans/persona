//go:build !windows

package devices

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/rpc"
)

// TestFakeStoppingNode is not a test: run as a subprocess (PERSONA_FAKE_STOPPING_NODE=<file>) it is a node on stdio
// that, once its stdin ends, takes a moment (stopping its conversations) and writes the file before it exits; with
// PERSONA_FAKE_NODE_HANG=1 it never exits on its own.
func TestFakeStoppingNode(t *testing.T) {
	mark := os.Getenv("PERSONA_FAKE_STOPPING_NODE")
	if mark == "" {
		t.Skip("helper process")
	}
	p := rpc.New("n", os.Stdout)
	p.OnRequest = func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		return map[string]any{"pid": os.Getpid()}, nil
	}
	_ = p.Serve(context.Background(), os.Stdin)
	if os.Getenv("PERSONA_FAKE_NODE_HANG") == "1" {
		select {} // hangs until the node kills it
	}
	time.Sleep(300 * time.Millisecond) // sleep-ok: the fake node takes a moment to exit, which the test measures
	_ = os.WriteFile(mark, []byte("stopped"), 0o600)
	os.Exit(0)
}

// stoppingNode runs m (this machine's own node, Kind local) on TestFakeStoppingNode and returns the node's pid once
// it answers, the file it writes as it ends, and what ends m's run.
func stoppingNode(t *testing.T, hang bool) (pid int, mark string, stop context.CancelFunc) {
	t.Helper()
	mark = filepath.Join(t.TempDir(), "stopped")
	m := newMachine(newTestDevices(t), MachineConfig{ID: "box", Name: "Box", Kind: "local"})
	m.nodeCmd = func(ctx context.Context) *exec.Cmd {
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFakeStoppingNode$")
		cmd.Env = append(os.Environ(), "PERSONA_FAKE_STOPPING_NODE="+mark)
		if hang {
			cmd.Env = append(cmd.Env, "PERSONA_FAKE_NODE_HANG=1")
		}
		return cmd
	}
	stop = runMachine(t, m)
	var err error
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); <-tick.C {
		var out struct {
			PID int `json:"pid"`
		}
		cctx, ccancel := context.WithTimeout(context.Background(), 2*time.Second)
		err = m.call(cctx, "ping", map[string]any{}, &out)
		ccancel()
		if err == nil && out.PID > 0 {
			return out.PID, mark, stop
		}
	}
	t.Fatalf("the node never answered: %v", err)
	return 0, "", nil
}

// The manager ends this machine's node with EOF on its stdin, not a kill: the node stops the assistant's
// conversations before it exits (node.Serve), which a SIGKILL never let it do, leaving them running beside the ones
// the next node started for the same conversations (#4259).
func TestLocalNodeEndsOnEOFNotKilled(t *testing.T) {
	_, mark, stop := stoppingNode(t, false)
	stop()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); <-tick.C {
		if _, err := os.Stat(mark); err == nil {
			return
		}
	}
	t.Fatal("the node was killed before it finished stopping")
}

// A local node that does not exit after its stdin ends is still killed, once localNodeStopGrace has passed.
func TestLocalNodeThatHangsIsKilledAfterTheGrace(t *testing.T) {
	old := localNodeStopGrace
	localNodeStopGrace = 300 * time.Millisecond
	t.Cleanup(func() { localNodeStopGrace = old })
	pid, _, stop := stoppingNode(t, true)
	stop()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); <-tick.C {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
	}
	t.Fatal("a node that ignored EOF is still running after the grace")
}
