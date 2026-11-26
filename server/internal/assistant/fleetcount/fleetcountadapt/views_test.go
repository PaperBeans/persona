package fleetcountadapt

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/nwverse/persona/server/internal/agentviewwire"
	"github.com/nwverse/persona/server/internal/assistant/fleetcount"
	"github.com/nwverse/persona/server/internal/proto"
)

// agentViewFixtures defines each session's five-phase view (spec/fixtures/agent-view/README.md); the clients' replays
// read the same cases.
const agentViewFixtures = "../../../../../spec/fixtures/agent-view/cases.json"

type agentViewCase struct {
	needsYouCase
	Expected struct {
		Views []struct {
			ID   string                  `json:"id"`
			View agentviewwire.AgentView `json:"view"`
		} `json:"views"`
	} `json:"expected"`
}

func readAgentViewCases(t *testing.T) []agentViewCase {
	t.Helper()
	b, err := os.ReadFile(agentViewFixtures)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Cases []agentViewCase `json:"cases"`
	}
	if err := json.Unmarshal(b, &file); err != nil || len(file.Cases) == 0 {
		t.Fatalf("%s: %v (%d cases)", agentViewFixtures, err, len(file.Cases))
	}
	return file.Cases
}

// prepared is a case's sessions as the manager has them before AttachViews: the quiet ones marked.
func (c *agentViewCase) prepared() []proto.Session {
	list := slices.Clone(c.Input.Sessions)
	for i := range list {
		list[i].Quiet = slices.Contains(c.Input.Quiet, list[i].MachineID+"/"+list[i].SessionID)
	}
	return list
}

// Every session's view is the fixture's: its phase, reason, counted and rank, through the manager's computation
// (AttachViews over the fixture's sessions and cards, as the hub sends them).
func TestAgentViewsFollowTheSharedFixtures(t *testing.T) {
	for _, c := range readAgentViewCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			list := c.prepared()
			AttachViews(list, c.muted, c.Input.Interactions)
			if len(c.Expected.Views) != len(list) {
				t.Fatalf("the case expects %d views for %d sessions", len(c.Expected.Views), len(list))
			}
			for i := range list {
				want := c.Expected.Views[i]
				if id := list[i].MachineID + "/" + list[i].SessionID; id != want.ID {
					t.Fatalf("view %d is %s's, the case says %s", i, id, want.ID)
				}
				got, _ := json.Marshal(list[i].View())
				exp, _ := json.Marshal(want.View)
				if string(got) != string(exp) {
					t.Errorf("%s: view %s\nwant %s", want.ID, got, exp)
				}
			}
		})
	}
}

// counted is fleetcount's decision exactly: on every case, the sessions whose view counts are the agents of the Agents
// list's counted rows, and as many as its Needs you number less the counted rows of cards from no session.
func TestAgentViewCountedIsTheBadgesDecision(t *testing.T) {
	cases := readAgentViewCases(t)
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			list := c.prepared()
			AttachViews(list, c.muted, c.Input.Interactions)
			var f Fleet
			for i := range list {
				f.Agent("Mac", &list[i], c.muted(&list[i]))
			}
			for i := range c.Input.Interactions {
				f.Card(&c.Input.Interactions[i], false)
			}
			rows := badgeRows{f: &f}
			f.decide(fleetcount.SurfaceBadge()).Accept(&rows)
			want := map[string]bool{}
			for i, row := range rows.rows {
				if rows.counted[i] && row.ID[:6] == "agent:" {
					want[row.ID[6:]] = true
				}
			}
			for i := range list {
				id := list[i].MachineID + "/" + list[i].SessionID
				if v := list[i].View(); v == nil || v.Counted != want[id] {
					t.Errorf("%s: view %+v, fleetcount counts it %v", id, v, want[id])
				}
			}
		})
	}
}

// A session fleetcount refuses (no session id) gets no view, and one that had a view loses it; the others still get
// theirs, written copy-on-write so another copy of a row keeps what it had.
func TestAttachViewsSkipsRefusedSessionsAndCopiesOnWrite(t *testing.T) {
	list := []proto.Session{
		{MachineID: "studio", SessionID: "s1", State: proto.StateWorking, Status: proto.StatusWorking},
		{MachineID: "studio", State: proto.StateWorking},
	}
	list[1].SetView(&agentviewwire.AgentView{Phase: agentviewwire.AgentPhaseIdle})
	kept := list[0]
	AttachViews(list, func(*proto.Session) bool { return false }, nil)
	if v := list[0].View(); v == nil || v.Phase != agentviewwire.AgentPhaseWorking || v.Reason.Kind != agentviewwire.AgentReasonTurn {
		t.Fatalf("a working session's view: %+v", v)
	}
	if list[1].View() != nil {
		t.Fatalf("a session with no id kept a view: %+v", list[1].View())
	}
	if kept.View() != nil {
		t.Fatal("writing the view changed another copy of the row")
	}
}
