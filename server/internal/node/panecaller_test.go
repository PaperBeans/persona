package node

import (
	"testing"

	"github.com/nwverse/persona/server/internal/host"
)

// An agent's MCP call names no session: the node takes the one its pane runs as the host shows it now, else as the
// hooks bound it; a stale binding does not win over the live agent.
func TestPaneCaller(t *testing.T) {
	n := &Node{panes: map[string]string{"w1:p1": "sess-hook", "w1:p2": "sess-stale"},
		agents: map[string]host.Agent{"sess-host": {PaneID: "w1:p2"}}}
	for pane, want := range map[string]string{"w1:p1": "sess-hook", "w1:p2": "sess-host", "w1:p3": "", "": ""} {
		if got := n.paneCaller(pane); got != want {
			t.Errorf("paneCaller(%q) = %q, want %q", pane, got, want)
		}
	}
}

// The agents' own tools that act for their caller get the pane's session; others, an assistant's call and a call
// that names its session go as they are.
func TestMCPCaller(t *testing.T) {
	n := &Node{panes: map[string]string{"w1:p1": "sess-pane"}}
	for _, c := range []struct {
		name, session string
		assistant     bool
		want          string
	}{
		{"report_to_supervisor", "", false, "sess-pane"},
		{"todo_decisions", "", false, "sess-pane"},
		{"todo_decision_add", "", false, "sess-pane"},
		{"todo_bind_thread", "", false, "sess-pane"},
		{"dispatch_start", "", false, "sess-pane"},
		{"dispatch_read", "", false, "sess-pane"},
		{"task_plan", "", false, "sess-pane"},
		{"task_review", "", false, "sess-pane"},
		{"task_spawn", "", false, "sess-pane"},
		{"task_plan", "", true, ""},
		{"kb_search", "", false, ""},
		{"todo_decisions", "", true, ""},
		{"todo_bind_thread", "", true, ""},
		{"todo_decisions", "sess-named", false, "sess-named"},
	} {
		if got := n.mcpCaller(c.name, c.session, "w1:p1", c.assistant); got != c.want {
			t.Errorf("mcpCaller(%q, %q, assistant=%v) = %q, want %q", c.name, c.session, c.assistant, got, c.want)
		}
	}
}

// An agent's tool list is asked for by its pane: the hub tells a task leader's tools from any other agent's by the
// session the pane runs. A session the request names is left as it is, and so is a pane no agent runs in.
func TestMCPToolsNameTheAgentOfThePane(t *testing.T) {
	n, hub := runtimeNode(t)
	n.mu.Lock()
	n.panes = map[string]string{"w1:p1": "sess-pane"}
	n.mu.Unlock()
	ask := mcpRelay(t, n, hub)
	check := func(req map[string]any, want string) {
		t.Helper()
		if got := ask(req); got["session_id"] != want {
			t.Errorf("%v: relayed session %v, want %q", req, got["session_id"], want)
		}
	}
	check(map[string]any{"kind": "mcp.tools", "pane": "w1:p1"}, "sess-pane")
	check(map[string]any{"kind": "mcp.tools", "pane": "w1:p1", "session_id": "named"}, "named")
	check(map[string]any{"kind": "mcp.tools", "pane": "w9:p9"}, "")
}
