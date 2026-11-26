//go:build omplive

package node

import (
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/herdr"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/transcript"
)

// A read-only attachment to a real, isolated herdr and runtime socket. The accompanying smoke script starts omp; this
// test only waits for its extension row to join the herdr pane. It never starts an agent, sends a message, or picks a
// model, so it cannot make a model request.
func TestOmpLiveAttachesToHerdrAndRuntime(t *testing.T) {
	herdrSocket, runtimeSocket := os.Getenv("HERDR_SOCKET_PATH"), os.Getenv("PERSONA_RUNTIME_SOCKET")
	if os.Getenv("PERSONA_OMP_LIVE") == "" || herdrSocket == "" || runtimeSocket == "" {
		t.Skip("PERSONA_OMP_LIVE / HERDR_SOCKET_PATH / PERSONA_RUNTIME_SOCKET not set")
	}
	n, home := testNode(t)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "claude"))
	t.Setenv("PI_CODING_AGENT_DIR", "")
	n.hosts = herdrHosts(herdr.New()) // HERDR_SOCKET_PATH selects the explicitly isolated real server.
	n.dir = filepath.Join(home, ".persona")
	n.panes, n.panePIDs, n.modes, n.driving = map[string]string{}, map[string]panePID{}, map[string]sessionModes{}, map[string]bool{}
	n.files, n.metas, n.bgScans = map[string]*transcript.File{}, map[string]quickMeta{}, map[string]*bgScan{}
	n.cdirs, n.codices = newClaudeDirs(), newCodexRegistry()
	n.cdirs.scan = func() map[int]procEnv { return nil } // only the explicitly attached herdr, not unrelated processes
	n.logger = log.New(io.Discard, "", 0)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	rh := NewRuntimeHost("omplive", runtimeSocket)
	rh.logger = log.New(io.Discard, "", 0)
	if err := rh.Listen(ctx); err != nil {
		t.Fatalf("listen on PERSONA_RUNTIME_SOCKET: %v", err)
	}
	n.runtimeSock = runtimeSocket
	rh.attach(n)
	t.Cleanup(func() { rh.detach(n) })

	// Do not use waitWithin here: live polling is bounded by ctx and driven only by a ticker/channel, never a fixed sleep.
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	findJoinedRow := func() (proto.Session, bool) {
		for _, s := range n.sessionList(ctx, true) {
			if s.Agent == ompAgent && s.PaneID != "" && s.SpaceKind == "herdr" && s.ReadOnlyReason == "" {
				return s, true
			}
		}
		return proto.Session{}, false
	}
	for {
		if s, ok := findJoinedRow(); ok {
			if owner, _, known := n.runtimes.belongs(s.SessionID); !known || owner == nil {
				t.Fatalf("the omp row has a pane but no connected extension: %+v", s)
			}
			t.Logf("LIVE omp %s joined herdr pane %s without sending", s.SessionID, s.PaneID)
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for an omp extension row joined to a herdr pane: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}
