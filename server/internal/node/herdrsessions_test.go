package node

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"slices"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/herdr"
	"github.com/nwverse/persona/server/internal/host"
)

func testHerdrNode(dir string) *Node {
	return &Node{
		dir:    dir,
		home:   dir,
		logger: log.New(io.Discard, "", 0),
		agents: map[string]host.Agent{},
		panes:  map[string]string{},
		hooks:  &hookTurns{},
		cdirs:  newClaudeDirs(),
		state:  newState(),
	}
}

func TestHerdrSessionsBlockedInHostDiscoveryAndLookup(t *testing.T) {
	own := newFakeHerdr(t)
	agentSrv := newFakeHerdr(t)
	buildSrv := newFakeHerdr(t)

	own.reply("session.snapshot", snapshot(
		[]any{map[string]any{"id": "w1", "label": "main"}},
		[]any{map[string]any{"id": "t1", "workspace_id": "w1", "label": "tab"}},
		[]any{map[string]any{"pane_id": "w1:p1", "tab_id": "t1", "workspace_id": "w1", "agent": "claude", "agent_status": "idle", "cwd": "/work/main"}},
		nil,
	))
	agentSrv.reply("session.snapshot", snapshot(
		[]any{map[string]any{"id": "w1", "label": "bot"}},
		[]any{map[string]any{"id": "t1", "workspace_id": "w1", "label": "tab"}},
		[]any{map[string]any{"pane_id": "w1:p1", "tab_id": "t1", "workspace_id": "w1", "agent": "claude", "agent_status": "working", "cwd": "/work/bot"}},
		nil,
	))
	buildSrv.reply("session.snapshot", snapshot(
		[]any{map[string]any{"id": "w1", "label": "ci"}},
		[]any{map[string]any{"id": "t1", "workspace_id": "w1", "label": "tab"}},
		[]any{map[string]any{"pane_id": "w1:p1", "tab_id": "t1", "workspace_id": "w1", "agent": "claude", "agent_status": "idle", "cwd": "/work/ci"}},
		nil,
	))

	c := &herdr.Client{Bin: "/nonexistent/herdr", Socket: own.sock}
	named := &herdrSessions{
		own: c,
		list: func(context.Context) ([]herdr.SessionInfo, error) {
			return []herdr.SessionInfo{
				{Name: "default", Default: true, Running: true, Socket: own.sock},
				{Name: "agent", Running: true, Socket: agentSrv.sock},
				{Name: "build", Running: true, Socket: buildSrv.sock},
			}, nil
		},
	}
	h := &herdrHost{c: c, named: named}

	// Default: both named sessions appear.
	snap, err := h.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	paneIDs := func(l *host.Layout) []string {
		out := make([]string, len(l.Agents))
		for i, a := range l.Agents {
			out[i] = a.PaneID
		}
		slices.Sort(out)
		return out
	}
	if got := paneIDs(snap); !slices.Equal(got, []string{"@agent:w1:p1", "@build:w1:p1", "w1:p1"}) {
		t.Fatalf("initial panes = %v", got)
	}

	// Blocking "agent" immediately drops it from snapshots and lookups.
	named.setBlocked([]string{"agent"})
	snap, err = h.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot after block: %v", err)
	}
	if got := paneIDs(snap); !slices.Equal(got, []string{"@build:w1:p1", "w1:p1"}) {
		t.Fatalf("after block panes = %v, want [@build:w1:p1 w1:p1]", got)
	}
	if ns := named.lookup(context.Background(), "agent"); ns != nil {
		t.Fatalf("blocked session lookup returned %+v, want nil", ns)
	}

	// Unblocking "agent" brings it back immediately on the next snapshot.
	named.setBlocked(nil)
	snap, err = h.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot after unblock: %v", err)
	}
	if got := paneIDs(snap); !slices.Equal(got, []string{"@agent:w1:p1", "@build:w1:p1", "w1:p1"}) {
		t.Fatalf("after unblock panes = %v", got)
	}
}

func TestBlockedHerdrSessionsPersistAndConfigure(t *testing.T) {
	dir := t.TempDir()
	n := testHerdrNode(dir)

	// Live agent in @agent:w1:p1 is remembered when "agent" is blocked.
	sid := "0e000000-0000-4000-8000-0000000000cc"
	n.mu.Lock()
	n.agents[sid] = host.Agent{PaneID: "@agent:w1:p1", Cwd: "/work/repo"}
	n.mu.Unlock()

	if _, err := request(t, n, "node.configure", map[string]any{"herdr_sessions_blocked": []string{"agent"}}); err != nil {
		t.Fatalf("node.configure: %v", err)
	}
	if !n.herdrSessionBlocked("agent") {
		t.Fatal("agent should be blocked")
	}
	if !n.herdrSessionIDBlocked(sid) {
		t.Fatal("live session in @agent pane should be remembered as blocked")
	}
	if !n.listBlocked(sid, "/work/repo") || !n.restBlocked(sid, "/work/repo", nil) || !n.sessionBlocked(sid, "/work/repo") {
		t.Fatal("listBlocked/restBlocked/sessionBlocked should block remembered session ID")
	}

	// Null or omitted herdr_sessions_blocked leaves the persisted list alone.
	for _, params := range []map[string]any{{"herdr_sessions_blocked": nil}, {"machine_id": "m1"}} {
		if _, err := request(t, n, "node.configure", params); err != nil {
			t.Fatalf("node.configure %v: %v", params, err)
		}
		if !n.herdrSessionBlocked("agent") {
			t.Fatalf("params %v cleared blocked herdr sessions", params)
		}
	}

	// Reload on a fresh node preserves both blocked session names and remembered session IDs.
	again := testHerdrNode(dir)
	again.loadBlockedHerdrSessions()
	if !again.herdrSessionBlocked("agent") || !again.herdrSessionIDBlocked(sid) {
		t.Fatal("reloaded node lost blocked herdr sessions or session IDs")
	}

	// Empty array clears blocks and immediately unblocks remembered session IDs.
	if _, err := request(t, again, "node.configure", map[string]any{"herdr_sessions_blocked": []string{}}); err != nil {
		t.Fatalf("clear node.configure: %v", err)
	}
	if again.herdrSessionBlocked("agent") || again.herdrSessionIDBlocked(sid) {
		t.Fatal("empty herdr_sessions_blocked should clear blocks")
	}
}

func TestBlockedHerdrSessionSkipsHooksAndOutsideClaudes(t *testing.T) {
	dir := t.TempDir()
	n := testHerdrNode(dir)
	n.setBlockedHerdrSessions([]string{"agent"})

	// Hook from @agent:w1:p1 returns nil (passthrough) and remembers the session ID.
	sid := "0e000000-0000-4000-8000-0000000000dd"
	in, _ := json.Marshal(hookInput{
		HookEventName: "PermissionRequest",
		SessionID:     sid,
		Cwd:           "/work/repo",
		ToolName:      "Bash",
	})
	out, err := n.handleHook(context.Background(), sockRequest{Kind: "hook", Pane: "@agent:w1:p1", Input: in})
	if err != nil || out != nil {
		t.Fatalf("blocked hook = %v, %v, want nil, nil", out, err)
	}
	if !n.herdrSessionIDBlocked(sid) {
		t.Fatal("hook session ID in blocked herdr session was not remembered")
	}

	// Outside Claude process running in HERDR_SESSION=agent is skipped and remembered.
	origEnv := paneEnvOfPID
	t.Cleanup(func() { paneEnvOfPID = origEnv })
	paneEnvOfPID = func(pid int) map[string]string {
		if pid == 4242 {
			return map[string]string{herdrPaneEnv: "w2:p1", herdrSessionEnv: "agent"}
		}
		return nil
	}
	sid2 := "0e000000-0000-4000-8000-0000000000ee"
	rows := n.claudeSessions([]claudeProc{{
		pid:        4242,
		SessionID:  sid2,
		Entrypoint: "cli",
		Kind:       "interactive",
		Cwd:        "/work/repo",
		Status:     "busy",
	}}, map[string]bool{}, map[string]bool{})
	if len(rows) != 0 {
		t.Fatalf("blocked outside claude listed: %+v", rows)
	}
	if !n.herdrSessionIDBlocked(sid2) {
		t.Fatal("outside claude session ID in blocked herdr session was not remembered")
	}

	// Kept sessions remain exempt even if seen in a blocked herdr session.
	n.setKept([]string{sid2})
	if n.herdrSessionIDBlocked(sid2) {
		t.Fatal("kept session should remain exempt")
	}
	_ = time.Second
}
