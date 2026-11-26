package proto

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/agentviewwire"
)

// A session's view is the top-level "view" key, written by appendSession as the reflect encoder writes it, read back
// whole, absent when the row has none, and set copy-on-write so another copy of the row keeps what it had.
func TestSessionViewWire(t *testing.T) {
	view := &agentviewwire.AgentView{Phase: agentviewwire.AgentPhaseNeedsYou, Counted: true, Rank: 0,
		Reason: &agentviewwire.AgentReason{Kind: agentviewwire.AgentReasonCards, N: 2}}
	s := Session{MachineID: "studio", SessionID: "s1", State: StateNeedsYou}
	before := s
	s.SetView(view)
	if before.View() != nil || s.View() != view {
		t.Fatal("SetView changed another copy of the row, or did not set it")
	}
	checkWire(t, "a session with a view", s)
	s.SetParentAgentID("p1")
	checkWire(t, "a session with a view and a hierarchy", s)
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"view":{"phase":"needs_you","reason":{"kind":"cards","n":2},"counted":true,"rank":0}`) {
		t.Fatalf("encoded %s", b)
	}
	var back Session
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if v := back.View(); v == nil || v.Phase != view.Phase || v.Reason == nil || v.Reason.N != 2 || !v.Counted {
		t.Fatalf("read back %+v", v)
	}
	s.SetView(nil)
	s.SetParentAgentID("")
	if s.Hierarchy != nil {
		t.Fatal("a row with no view and nothing else kept a hierarchy")
	}
	if b, _ := json.Marshal(s); strings.Contains(string(b), `"view"`) {
		t.Fatalf("a row with no view wrote one: %s", b)
	}
}
