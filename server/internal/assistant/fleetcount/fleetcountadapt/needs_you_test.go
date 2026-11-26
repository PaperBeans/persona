package fleetcountadapt

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/nwverse/persona/server/internal/assistant/fleetcount"
	"github.com/nwverse/persona/server/internal/domainkit/instant"
	"github.com/nwverse/persona/server/internal/domainkit/outcome"
	"github.com/nwverse/persona/server/internal/domainkit/slot"
	"github.com/nwverse/persona/server/internal/proto"
)

// needsYouFixtures defines the Agents list's Needs you (spec/fixtures/needs-you/README.md); the iOS and Android
// replays read the same cases.
const needsYouFixtures = "../../../../../spec/fixtures/needs-you/cases.json"

type needsYouRow struct {
	ID    string   `json:"id"`
	Cards []string `json:"cards"`
}

type needsYouCase struct {
	Name  string `json:"name"`
	Input struct {
		Sessions     []proto.Session     `json:"sessions"`
		Interactions []proto.Interaction `json:"interactions"`
		Mutes        struct {
			Sessions []string `json:"sessions"`
			Spaces   []string `json:"spaces"`
			Allowed  []string `json:"allowed"`
		} `json:"mutes"`
		Quiet []string `json:"quiet"`
	} `json:"input"`
	Expected struct {
		Rows      []needsYouRow `json:"rows"`
		BadgeRows []needsYouRow `json:"badge_rows"`
		Badge     int           `json:"badge"`
		Header    int           `json:"header"`
	} `json:"expected"`
}

// muted is the fixture's pin rule: a session pinned muted, or one in a muted space that was not turned back on.
func (c needsYouCase) muted(s *proto.Session) bool {
	key := s.MachineID + "/" + s.SessionID
	m := c.Input.Mutes
	return slices.Contains(m.Sessions, key) ||
		s.SpaceName != "" && slices.Contains(m.Spaces, s.MachineID+"/"+s.SpaceName) && !slices.Contains(m.Allowed, key)
}

// fleet is the case's sessions and cards as the hub sends them, read through this adapter.
func (c needsYouCase) fleet(t *testing.T) *Fleet {
	t.Helper()
	var f Fleet
	for i := range c.Input.Sessions {
		s := &c.Input.Sessions[i]
		s.Quiet = slices.Contains(c.Input.Quiet, s.MachineID+"/"+s.SessionID)
		if _, failed := outcome.Unpack(f.Agent("Mac", s, c.muted(s))); failed != nil {
			t.Fatalf("session %s: %s", s.SessionID, Log(failed))
		}
	}
	for i := range c.Input.Interactions {
		if _, failed := outcome.Unpack(f.Card(&c.Input.Interactions[i], false)); failed != nil {
			t.Fatalf("card %s: %s", c.Input.Interactions[i].ID, Log(failed))
		}
	}
	return &f
}

// badgeRows is the Agents list as a test reads it from a tally: each row with its cards, and which rows count.
type badgeRows struct {
	f       *Fleet
	rows    []needsYouRow
	counted []bool
	total   int
}

func (r *badgeRows) agentRow(agent slot.Slot, why fleetcount.Why, cards ...string) {
	s := r.f.seats[agent.Wire()].session
	r.rows = append(r.rows, needsYouRow{ID: "agent:" + s.MachineID + "/" + s.SessionID, Cards: cards})
	r.counted = append(r.counted, why.Counted())
}

func (r *badgeRows) VisitAgent(agent slot.Slot, why fleetcount.Why, _ instant.Instant) {
	r.agentRow(agent, why)
}

func (r *badgeRows) VisitAgentByCard(agent, card slot.Slot, why fleetcount.Why) {
	r.agentRow(agent, why, r.f.ixs[card.Wire()].ix.ID)
}

func (r *badgeRows) VisitCard(card slot.Slot, why fleetcount.Why) {
	id := r.f.ixs[card.Wire()].ix.ID
	r.rows = append(r.rows, needsYouRow{ID: "card:" + id, Cards: []string{id}})
	r.counted = append(r.counted, why.Counted())
}

func (r *badgeRows) VisitCardUnder(card slot.Slot) {
	last := &r.rows[len(r.rows)-1]
	last.Cards = append(last.Cards, r.f.ixs[card.Wire()].ix.ID)
}

func (r *badgeRows) VisitTotal(total fleetcount.RowCount) { r.total = total.Wire() }

// The Agents list's rows, the badge's rows and the badge are fleetcount's, from the fixture's sessions and cards as
// the hub sends them, read through this adapter: the server's replay of the definition the clients replay.
func TestAgentsNeedsYouFollowsTheSharedFixtures(t *testing.T) {
	for _, c := range needsYouCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			f := c.fleet(t)
			got := badgeRows{f: f}
			f.decide(fleetcount.SurfaceBadge()).Accept(&got)
			var badge []needsYouRow
			for i, row := range got.rows {
				if got.counted[i] {
					badge = append(badge, row)
				}
			}
			if !sameRows(got.rows, c.Expected.Rows) || !sameRows(badge, c.Expected.BadgeRows) ||
				got.total != c.Expected.Badge || got.total != c.Expected.Header {
				t.Fatalf("rows %v\n badge rows %v\n badge %d\nwant rows %v\n badge rows %v\n badge %d, header %d",
					got.rows, badge, got.total, c.Expected.Rows, c.Expected.BadgeRows, c.Expected.Badge, c.Expected.Header)
			}
		})
	}
}

// needsYouCases are the cases of spec/fixtures/needs-you.
func needsYouCases(t *testing.T) []needsYouCase {
	t.Helper()
	b, err := os.ReadFile(needsYouFixtures)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Cases []needsYouCase `json:"cases"`
	}
	if err := json.Unmarshal(b, &file); err != nil || len(file.Cases) == 0 {
		t.Fatalf("%s: %v (%d cases)", needsYouFixtures, err, len(file.Cases))
	}
	return file.Cases
}

func sameRows(a, b []needsYouRow) bool {
	return slices.EqualFunc(a, b, func(x, y needsYouRow) bool {
		return x.ID == y.ID && slices.Equal(x.Cards, y.Cards)
	})
}
