package assistant

import (
	"encoding/json"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// An Agent's address for a conversation its computer now lists under another id still names it (#14395): an
// Antigravity CLI conversation moved from its pane's row to its own, and the row listed names the pane's id among its
// aliases. a2a_send starts the task on that row; an id no row names, or two rows do, is left as the Agent gave it.
func TestA2ASendFollowsAnIDTheConversationWentBy(t *testing.T) {
	s, h, st, cfg, parent, to := a2aFixture(t)
	ctx := t.Context()
	const pane = "agy-w1-p1"
	h.sessions[to.Machine][0].SetAliases([]string{pane})

	for _, c := range []struct {
		name     string
		ref, out a2aRef
	}{
		{"listed under its own id", to, to},
		{"an id it went by", a2aRef{to.Machine, pane}, to},
		{"an id no row names", a2aRef{to.Machine, "agy-w1-p9"}, a2aRef{to.Machine, "agy-w1-p9"}},
		{"another computer's", a2aRef{parent.Ref.Machine, pane}, a2aRef{parent.Ref.Machine, pane}},
		{"a computer out of scope", a2aRef{"elsewhere", pane}, a2aRef{"elsewhere", pane}},
		{"no session", a2aRef{to.Machine, ""}, a2aRef{to.Machine, ""}},
	} {
		if got := s.a2aResolve(ctx, cfg, c.ref); got != c.out {
			t.Errorf("%s: %+v resolved to %+v, want %+v", c.name, c.ref, got, c.out)
		}
	}

	args, _ := json.Marshal(map[string]string{"machine_id": to.Machine, "session_id": pane, "message_id": "by-old-id", "text": "Run the checks."})
	out, err := s.a2aToolCall(ctx, st, cfg, parent, "a2a_send", args)
	if err != nil {
		t.Fatalf("a2a_send to an id the conversation went by: %v", err)
	}
	if view := out.(a2aTaskView); view.Target != to {
		t.Fatalf("task target %+v, want the row listed now %+v", view.Target, to)
	}

	// A row listed under the id itself is the one meant, whatever another row names.
	h.sessions[to.Machine] = append(h.sessions[to.Machine], proto.Session{SessionID: pane, Status: proto.StatusIdle, Agent: "agy"})
	if got := s.a2aResolve(ctx, cfg, a2aRef{to.Machine, pane}); got.Session != pane {
		t.Fatalf("a listed id resolved to %+v", got)
	}
	// Two rows naming one id: neither is guessed.
	h.sessions[to.Machine] = h.sessions[to.Machine][:1]
	second := proto.Session{SessionID: "runtime.worker-0002", Status: proto.StatusIdle, Agent: "pi", SourceKind: "runtime",
		Capabilities: &proto.SessionCapabilities{Send: true, Read: true}}
	second.SetAliases([]string{pane})
	h.sessions[to.Machine] = append(h.sessions[to.Machine], second)
	if got := s.a2aResolve(ctx, cfg, a2aRef{to.Machine, pane}); got.Session != pane {
		t.Fatalf("an id two rows name resolved to %+v", got)
	}
}
