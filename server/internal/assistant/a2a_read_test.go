package assistant

import (
	"encoding/json"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// itemReadTurns are the turns each session.items read asked for, in order.
func itemReadTurns(h *a2aTestHub) []float64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []float64
	for _, c := range h.calls {
		if c.Method != DeviceSessionItems {
			continue
		}
		var p struct {
			Turns float64 `json:"turns"`
		}
		_ = json.Unmarshal(c.Params, &p)
		out = append(out, p.Turns)
	}
	return out
}

// a2a_read (#13831): a session in scope reads another Agent's recent conversation as text, two turns unless it asks
// for more, never more than ten.
func TestA2AReadReturnsAnAgentsRecentConversation(t *testing.T) {
	s, h, _, _, parent, child := a2aFixture(t)
	ctx := t.Context()
	h.mu.Lock()
	h.sessions[child.Machine][0].Title, h.sessions[child.Machine][0].Cwd = "Login fix", "/code/app"
	h.mu.Unlock()
	h.setItems(
		proto.Item{Role: "user", Kind: proto.KindText, Text: "Fix the flaky login test."},
		proto.Item{Role: "assistant", Kind: proto.KindToolUse, ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"go test ./login"}`)},
		proto.Item{Role: "assistant", Kind: proto.KindText, Text: "The test waited on a fixed sleep; it now waits for the server."},
	)
	read := func(args string) proto.ToolResult {
		return s.mcpCall(ctx, parent.Ref.Machine, parent.Ref.Session, "a2a_read", json.RawMessage(args), false)
	}
	r := read(`{"machine_id":"box","session_id":"runtime.worker-0001"}`)
	want := "Login fix · pi on box (idle, /code/app)\n\nUser: Fix the flaky login test.\n\n→ Bash go test ./login\n" +
		"Agent: The test waited on a fixed sleep; it now waits for the server.\n\n"
	if r.IsError || resultText(r) != want {
		t.Fatalf("read:\n%q\nwant\n%q", resultText(r), want)
	}
	if r := read(`{"machine_id":"box","session_id":"runtime.worker-0001","turns":50}`); r.IsError {
		t.Fatal(resultText(r))
	}
	if got := itemReadTurns(h); len(got) != 2 || got[0] != a2aReadTurns || got[1] != a2aReadMaxTurns {
		t.Fatalf("turns asked of the node: %v", got)
	}
}

// What a2a_send may not address a2a_read may not read, nor the assistant's own conversations; a read-only session in
// scope can be read, though it takes no messages.
func TestA2AReadKeepsToTheA2AScope(t *testing.T) {
	s, h, _, _, parent, child := a2aFixture(t)
	ctx := t.Context()
	h.mu.Lock()
	h.sessions[child.Machine] = append(h.sessions[child.Machine],
		proto.Session{SessionID: "assistant-0001", Status: proto.StatusIdle, Assistant: true},
		proto.Session{SessionID: "health-0001", Status: proto.StatusIdle, Agent: "claude", Space: "health"},
		proto.Session{SessionID: "archived-0001", Status: proto.StatusIdle, Agent: "claude", Archived: true},
		proto.Session{SessionID: "subagent-0001", Status: proto.StatusIdle, Agent: "claude", IsSubagent: true},
		proto.Session{SessionID: "history-0001", Status: proto.StatusIdle, Agent: "codex", SourceKind: "codex_history",
			Capabilities: &proto.SessionCapabilities{Read: true}})
	h.sessions["far"] = []proto.Session{{SessionID: "far-0001", Status: proto.StatusIdle, Agent: "claude"}}
	h.mu.Unlock()
	h.setItems(proto.Item{Role: "assistant", Kind: proto.KindText, Text: "Done."})
	call := func(machine, session, args string) proto.ToolResult {
		return s.mcpCall(ctx, machine, session, "a2a_read", json.RawMessage(args), false)
	}
	for args, want := range map[string]error{
		`{"machine_id":"mac","session_id":"caller-0001"}`:    errA2AInvalid,     // itself
		`{"machine_id":"box"}`:                               errA2AInvalid,     // no session
		`{"machine_id":"far","session_id":"far-0001"}`:       errA2AUnavailable, // a computer outside the scope
		`{"machine_id":"box","session_id":"assistant-0001"}`: errA2ANoSession,
		`{"machine_id":"box","session_id":"health-0001"}`:    errA2ANoSession,
		`{"machine_id":"box","session_id":"archived-0001"}`:  errA2ANoSession,
		`{"machine_id":"box","session_id":"subagent-0001"}`:  errA2ANoSession,
		`{"machine_id":"box","session_id":"nobody-0001"}`:    errA2ANoSession,
	} {
		if r := call(parent.Ref.Machine, parent.Ref.Session, args); !r.IsError || resultText(r) != want.Error() {
			t.Errorf("%s: %q, want %q", args, resultText(r), want)
		}
	}
	if r := call("far", "far-0001", `{"machine_id":"box","session_id":"runtime.worker-0001"}`); !r.IsError ||
		resultText(r) != errA2AUnavailable.Error() {
		t.Errorf("a caller outside the scope read: %q", resultText(r))
	}
	if n := len(itemReadTurns(h)); n != 0 {
		t.Fatalf("%d refused reads reached the node", n)
	}
	// Untitled and with no folder: the head names the session by its id.
	if r := call(parent.Ref.Machine, parent.Ref.Session, `{"machine_id":"box","session_id":"history-0001"}`); r.IsError ||
		resultText(r) != "history-0001 · codex on box (idle)\n\nAgent: Done.\n\n" {
		t.Fatalf("read-only session: %q", resultText(r))
	}
	if _, err := s.a2aSession(ctx, s.a2aConfig(ctx), a2aRef{"box", "history-0001"}); err == nil {
		t.Fatal("a read-only session became a send target")
	}
}
