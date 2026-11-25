package assistant

import (
	"bytes"
	"encoding/json"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistant/fleetcount/fleetcountadapt"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
)

// needsYouFixtures defines the Agents list's Needs you (spec/fixtures/needs-you/README.md), which agents_needs_you is.
const needsYouFixtures = "../../../spec/fixtures/needs-you/cases.json"

type needsYouCase struct {
	Name  string `json:"name"`
	Input struct {
		Sessions     []proto.Session     `json:"sessions"`
		Interactions []proto.Interaction `json:"interactions"`
		Mutes        Pins                `json:"mutes"`
		Quiet        []string            `json:"quiet"`
	} `json:"input"`
	Expected struct {
		BadgeRows []struct {
			ID    string   `json:"id"`
			Cards []string `json:"cards"`
		} `json:"badge_rows"`
		Badge int `json:"badge"`
	} `json:"expected"`
}

func readNeedsYouCases(t *testing.T) []needsYouCase {
	t.Helper()
	b, err := os.ReadFile(needsYouFixtures)
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Cases []needsYouCase `json:"cases"`
	}
	if err := json.Unmarshal(b, &f); err != nil || len(f.Cases) == 0 {
		t.Fatalf("%s: %v (%d cases)", needsYouFixtures, err, len(f.Cases))
	}
	return f.Cases
}

// On one live round, agents_needs_you is the needs-you fixture's badge over the round's inputs: the case's sessions as
// fleetInputsInto hands them over (quiet and directory-hidden ones left out, muted by the owner's mutes) and its cards.
// Its counted rows are the badge's rows, and totals repeat its number.
func TestCountsViewFollowsTheSharedFixtures(t *testing.T) {
	for _, c := range readNeedsYouCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			s := newFleetTestNoStore(t).s
			setMutes(s, c.Input.Mutes)
			var agents []fleetInput
			for i := range c.Input.Sessions {
				sess := &c.Input.Sessions[i]
				sess.Quiet = slices.Contains(c.Input.Quiet, sess.MachineID+"/"+sess.SessionID)
				if fleetcountadapt.Visibility(sess).LeftOut() { // as fleetInputsInto
					continue
				}
				agents = append(agents, fleetInput{Machine: "Mac", Session: sess,
					Muted: s.mutedAs(sess.MachineID, sess.SessionID, sess.SpaceName)})
			}
			var r fleetRound
			_, v := r.linkAndCounts("en", agents, c.Input.Interactions, nil)
			count := v.Counts.AgentsNeedsYou
			if count == nil || v.Totals == nil || v.Totals.AgentsNeedsYou == nil {
				t.Fatalf("no agents_needs_you: %+v", v)
			}
			var rows, want []string
			for _, k := range count.Keys {
				row := string(k.ID)
				for _, card := range k.Cards {
					row += " " + string(card)
				}
				rows = append(rows, row)
			}
			for _, k := range c.Expected.BadgeRows {
				row := k.ID
				for _, card := range k.Cards {
					row += " " + card
				}
				want = append(want, row)
			}
			if count.N != c.Expected.Badge || *v.Totals.AgentsNeedsYou != count.N || !slices.Equal(rows, want) {
				t.Fatalf("n %d (totals %d), rows %q; want the badge %d, rows %q", count.N, *v.Totals.AgentsNeedsYou,
					rows, c.Expected.Badge, want)
			}
		})
	}
}

// countsRound is one of liveLoop's rounds over s's fleet, the Live Activity left out: the summary and the counts view
// made, and the view told.
func countsRound(s *Service, r *fleetRound) {
	agents, ixs, told := s.fleetInputs()
	_, v := r.linkAndCounts("en", agents, ixs, told)
	s.tellCounts(v)
}

// countsTold is every counts view told the hub so far, decoded as the hub's relay will read it.
func countsTold(t *testing.T, ft *fleetTest) ([]assistantwire.CountsView, []json.RawMessage) {
	t.Helper()
	raw := ft.toldOf(assistantwire.EventCountsView)
	var out []assistantwire.CountsView
	for _, b := range raw {
		var v assistantwire.CountsView
		d := json.NewDecoder(bytes.NewReader(b))
		d.DisallowUnknownFields()
		if err := d.Decode(&v); err != nil {
			t.Fatalf("%s: %v", b, err)
		}
		out = append(out, v)
	}
	return out, raw
}

// The manager tells its view when its counts change: a new rev in this run's epoch, the build that made it and when.
// A round that changes nothing tells nothing; after a handshake the last view is told again as it was.
func TestTheCountsViewIsToldWhenItChanges(t *testing.T) {
	ft := newFleetTestNoStore(t)
	s := ft.s
	s.build = "1.2.3"
	at := time.Now().Add(-time.Hour).UTC()
	waiting := func(ids ...string) { // each session waits on a card of its own
		s.fleet.mu.Lock()
		clear(s.ixMapLocked())
		s.fleet.mu.Unlock()
		var list []proto.Session
		for _, id := range ids {
			list = append(list, proto.Session{SessionID: id, Cwd: "/home/user/project", Status: proto.StatusIdle, UpdatedAt: at})
			putPending(s, &pending{ix: proto.Interaction{ID: "ix-" + id, MachineID: "mac", SessionID: id,
				Kind: proto.IxPermission, ToolName: "Bash", Summary: "npm test", CreatedAt: at}})
		}
		ft.d.setSessions("mac", list)
	}
	var r fleetRound
	waiting("s-1")
	before := time.Now().UnixMilli()
	countsRound(s, &r)
	views, _ := countsTold(t, ft)
	if len(views) != 1 {
		t.Fatalf("told %d views, want 1", len(views))
	}
	first := views[0]
	if first.Epoch == "" || first.Rev != 1 || first.AsOf < before || first.Build != "1.2.3" ||
		first.Counts.AgentsNeedsYou == nil || first.Counts.AgentsNeedsYou.N != 1 ||
		len(first.Counts.AgentsNeedsYou.Keys) != 1 || first.Counts.AgentsNeedsYou.Keys[0].ID != "agent:mac/s-1" ||
		!slices.Equal(first.Counts.AgentsNeedsYou.Keys[0].Cards, []assistantwire.CountsCardID{"ix-s-1"}) ||
		*first.Totals.AgentsNeedsYou != 1 {
		_, raw := countsTold(t, ft)
		t.Fatalf("the first view: %s", raw[0])
	}
	countsRound(s, &r)
	countsRound(s, &r)
	if views, _ := countsTold(t, ft); len(views) != 1 {
		t.Fatalf("rounds that changed nothing told %d views", len(views)-1)
	}
	s.liveChanged(true) // a handshake: the hub may have restarted
	countsRound(s, &r)
	views, raw := countsTold(t, ft)
	if len(views) != 2 || !bytes.Equal(raw[0], raw[1]) {
		t.Fatalf("after a handshake: %d views, the last %s", len(views), raw[len(raw)-1])
	}
	waiting("s-1", "s-2")
	countsRound(s, &r)
	views, _ = countsTold(t, ft)
	last := views[len(views)-1]
	if len(views) != 3 || last.Epoch != first.Epoch || last.Rev != 2 || last.Counts.AgentsNeedsYou.N != 2 ||
		*last.Totals.AgentsNeedsYou != 2 {
		t.Fatalf("after a change: %d views, the last %+v", len(views), last)
	}
	waiting()
	countsRound(s, &r)
	views, raw = countsTold(t, ft)
	if len(views) != 4 || views[3].Rev != 3 || views[3].Counts.AgentsNeedsYou == nil || views[3].Counts.AgentsNeedsYou.N != 0 ||
		*views[3].Totals.AgentsNeedsYou != 0 {
		t.Fatalf("nothing waiting is told as 0, never left out: %s", raw[len(raw)-1])
	}
}

// liveLoop tells the view in its round, after the Live Activity's state.
func TestLiveLoopTellsTheCountsView(t *testing.T) {
	ft := newFleetTestNoStore(t)
	at := time.Now().Add(-time.Hour).UTC()
	ft.d.setSessions("mac", []proto.Session{{SessionID: "s-1", Cwd: "/home/user/project", Status: proto.StatusIdle,
		State: proto.StateNeedsYou, StateSince: at, UpdatedAt: at}})
	startAttention(t, ft.s)
	ft.s.liveChanged(true)
	waitUntil(t, func() bool { return len(ft.toldOf(assistantwire.EventCountsView)) > 0 })
	ft.f.mu.Lock()
	defer ft.f.mu.Unlock()
	live, counts := -1, -1
	for i, told := range ft.f.told {
		switch told.event {
		case assistantwire.EventFleetLive:
			live = i
		case assistantwire.EventCountsView:
			if counts < 0 {
				counts = i
			}
		}
	}
	if live < 0 || live > counts {
		t.Fatalf("the Live Activity's state at %d, the counts view at %d: the view goes after the state", live, counts)
	}
}

// The manager names the capability that tells clients its views can be drawn.
func TestTheHelloNamesTheCountsView(t *testing.T) {
	b := NewBusHub(BusOptions{Hub: "http://localhost:1", Token: "t"})
	p, _ := b.client.HelloParams().(map[string]any)
	caps, _ := p["capabilities"].([]string)
	if !slices.Contains(caps, assistantwire.CapabilityCountsView) {
		t.Fatalf("the hello does not name %s: %v", assistantwire.CapabilityCountsView, caps)
	}
}
