package fleetcountadapt

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistant/fleetcount"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/domainkit/defect"
	"github.com/nwverse/persona/server/internal/domainkit/opt"
	"github.com/nwverse/persona/server/internal/domainkit/outcome"
	"github.com/nwverse/persona/server/internal/proto"
)

// surfacesTable is the counts surface table, whose counts name the function that writes each one (producer).
const surfacesTable = "../../../../../spec/fixtures/counts/surfaces.json"

// thisPackage is this package's directory as the table names files.
const thisPackage = "server/internal/assistant/fleetcount/fleetcountadapt"

// todoCountsFixtures are the to-do list's counts (spec/fixtures/todo-counts/README.md): what GET /api/v1/todos
// answers for each case's list, todos_needs_you's number.
const todoCountsFixtures = "../../../../../spec/fixtures/todo-counts/todo_counts.json"

// badgeView is what View writes of the Agents list's tally of f's agents and cards, before the to-dos were counted.
func badgeView(f *Fleet) assistantwire.CountsView {
	return f.View(f.decide(fleetcount.SurfaceBadge()), opt.None[assistantwire.TodoCounts]())
}

// todoCountsCases are the to-do list's counts of each todo-counts case, by name.
func todoCountsCases(t *testing.T) map[string]assistantwire.TodoCounts {
	t.Helper()
	b, err := os.ReadFile(todoCountsFixtures)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Cases []struct {
			Name     string `json:"name"`
			Expected struct {
				Counts assistantwire.TodoCounts `json:"counts"`
			} `json:"expected"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(b, &file); err != nil || len(file.Cases) == 0 {
		t.Fatalf("%s: %v (%d cases)", todoCountsFixtures, err, len(file.Cases))
	}
	out := map[string]assistantwire.TodoCounts{}
	for _, c := range file.Cases {
		out[c.Name] = c.Expected.Counts
	}
	return out
}

// todos_needs_you is the to-do list's own number, as GET /api/v1/todos answers it for each todo-counts case: n is its
// needs_you and totals repeat it, with no row listed (the list's counts carry none), so all of n is more; the agents'
// count beside it is the one View writes without the to-dos. Before the to-dos are counted the view carries neither
// the count nor its total: unknown, never 0.
func TestViewWritesTheToDoListsNumber(t *testing.T) {
	f := waitingFleet(t, "studio", 2, 1, shortIDs)
	agents := badgeView(f)
	if agents.Counts.TodosNeedsYou != nil || agents.Totals.TodosNeedsYou != nil {
		t.Fatalf("to-dos not counted yet, yet the view has %+v, totals %+v", agents.Counts.TodosNeedsYou, agents.Totals)
	}
	for name, counts := range todoCountsCases(t) {
		t.Run(name, func(t *testing.T) {
			v := f.View(f.decide(fleetcount.SurfaceBadge()), opt.Some(counts))
			got := v.Counts.TodosNeedsYou
			if got == nil || v.Totals.TodosNeedsYou == nil || got.N != counts.NeedsYou || *v.Totals.TodosNeedsYou != got.N ||
				got.More != got.N || got.Keys != nil || got.Held != nil {
				b, _ := json.Marshal(v)
				t.Fatalf("view %s; want todos_needs_you %d with no rows", b, counts.NeedsYou)
			}
			if !reflect.DeepEqual(v.Counts.AgentsNeedsYou, agents.Counts.AgentsNeedsYou) ||
				*v.Totals.AgentsNeedsYou != *agents.Totals.AgentsNeedsYou {
				t.Fatalf("the agents' count moved with the to-dos: %+v", v.Counts.AgentsNeedsYou)
			}
		})
	}
}

// The view of each needs-you case is the case's Agents list as the fixture defines it: its counted rows (badge_rows)
// as keys with their cards, its number as n and in totals, and its other rows as held, muted when the owner muted the
// agent and held otherwise, but a quiet task's, which the view does not list.
func TestViewFollowsTheSharedFixtures(t *testing.T) {
	for _, c := range needsYouCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			v := badgeView(c.fleet(t))
			got := v.Counts.AgentsNeedsYou
			if got == nil || v.Totals == nil || v.Totals.AgentsNeedsYou == nil {
				t.Fatalf("no agents_needs_you in %+v", v)
			}
			var keys []needsYouRow
			for _, k := range got.Keys {
				row := needsYouRow{ID: string(k.ID)}
				for _, card := range k.Cards {
					row.Cards = append(row.Cards, string(card))
				}
				keys = append(keys, row)
			}
			var held, wantHeld []string
			for _, h := range got.Held {
				held = append(held, string(h.ID)+" "+string(h.Why))
			}
			counted := map[string]bool{}
			for _, row := range c.Expected.BadgeRows {
				counted[row.ID] = true
			}
			for _, row := range c.Expected.Rows {
				if counted[row.ID] {
					continue
				}
				why := "held"
				if s := c.session(row.ID); s != nil {
					if slices.Contains(c.Input.Quiet, s.MachineID+"/"+s.SessionID) {
						continue
					}
					if c.muted(s) {
						why = "muted"
					}
				}
				wantHeld = append(wantHeld, row.ID+" "+why)
			}
			if got.N != c.Expected.Badge || *v.Totals.AgentsNeedsYou != got.N || got.More != 0 ||
				!sameRows(keys, c.Expected.BadgeRows) || !slices.Equal(held, wantHeld) {
				g, _ := json.Marshal(v)
				t.Fatalf("view %s\nwant n %d, keys %v, held %v", g, c.Expected.Badge, c.Expected.BadgeRows, wantHeld)
			}
		})
	}
}

// session is the case's session a row key names, nil for a card's row.
func (c needsYouCase) session(key string) *proto.Session {
	for i := range c.Input.Sessions {
		if s := &c.Input.Sessions[i]; key == "agent:"+s.MachineID+"/"+s.SessionID {
			return s
		}
	}
	return nil
}

// ids are a test fleet's session and card ids for agent i.
type ids func(i int) (session, card string)

// longIDs are ids as long as the ones the clients make, suffix appended to each session; shortIDs are as short as ids
// get.
func longIDs(suffix string) ids {
	return func(i int) (string, string) {
		return fmt.Sprintf("0b8f6f2c-5d1e-4a8e-9c1f-%012d%s", i, suffix), fmt.Sprintf("7c9e6679-7425-40de-944b-%012d", i)
	}
}

func shortIDs(i int) (string, string) { return fmt.Sprintf("s%03d", i), fmt.Sprintf("c%03d", i) }

// waitingFleet is n agents that wait for the owner on machine, each with one card, the oldest first (minute i), and
// held more muted ones after them.
func waitingFleet(t *testing.T, machine string, n, held int, id ids) *Fleet {
	t.Helper()
	base := time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)
	var f Fleet
	for i := range n + held {
		at := base.Add(time.Duration(i) * time.Minute)
		session, card := id(i)
		s := &proto.Session{MachineID: machine, SessionID: session, State: proto.StateNeedsYou, StateSince: at, UpdatedAt: at}
		if _, failed := outcome.Unpack(f.Agent("Mac", s, i >= n)); failed != nil {
			t.Fatal(Log(failed))
		}
		ix := &proto.Interaction{ID: card, MachineID: machine, SessionID: session, Kind: proto.IxPermission, CreatedAt: at}
		if _, failed := outcome.Unpack(f.Card(ix, false)); failed != nil {
			t.Fatal(Log(failed))
		}
	}
	return &f
}

// A view stays within assistantwire.CountsViewMaxBytes whatever waits, with an epoch, rev, as_of and build as large
// as the manager makes them: keys and held list at most 200 rows, the oldest first, fewer when the rows would not fit,
// and more is the counted rows keys leaves out. A key with bytes JSON escapes still fits.
func TestViewKeepsWithinItsBound(t *testing.T) {
	for _, c := range []struct {
		name           string
		machine        string
		waiting, held  int
		id             ids
		keys, heldRows int // how many rows each list holds, -1: fewer than there are, as many as fit
	}{
		{name: "a few", machine: "studio", waiting: 3, held: 2, id: longIDs(""), keys: 3, heldRows: 2},
		{name: "past 200 short rows", machine: "m", waiting: 250, held: 10, id: shortIDs, keys: 200, heldRows: 10},
		{name: "long ids past the bytes", machine: "studio-mac-mini.example.com", waiting: 300, held: 250,
			id: longIDs(""), keys: -1, heldRows: 0},
		{name: "escaped bytes", machine: "<studio>&<mac>", waiting: 100, held: 100, id: longIDs("<&>"), keys: -1,
			heldRows: 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			v := badgeView(waitingFleet(t, c.machine, c.waiting, c.held, c.id))
			count := v.Counts.AgentsNeedsYou
			if count.N != c.waiting || len(count.Keys)+count.More != count.N || *v.Totals.AgentsNeedsYou != count.N {
				t.Fatalf("n %d, %d keys and %d more, totals %d; want n %d", count.N, len(count.Keys), count.More,
					*v.Totals.AgentsNeedsYou, c.waiting)
			}
			if c.keys >= 0 && len(count.Keys) != c.keys || c.keys < 0 && (len(count.Keys) == 0 || len(count.Keys) >= c.waiting) {
				t.Errorf("%d keys of %d waiting", len(count.Keys), c.waiting)
			}
			if len(count.Held) != c.heldRows {
				t.Errorf("%d held of %d, want %d", len(count.Held), c.held, c.heldRows)
			}
			for i, k := range count.Keys { // the oldest first, each with its card
				session, card := c.id(i)
				if want := "agent:" + c.machine + "/" + session; string(k.ID) != want || !slices.Equal(k.Cards, []assistantwire.CountsCardID{assistantwire.CountsCardID(card)}) {
					t.Fatalf("key %d is %s with %v, want %s with %s", i, k.ID, k.Cards, want, card)
				}
			}
			for i, h := range count.Held {
				session, _ := c.id(c.waiting + i)
				if h.Why != assistantwire.CountsHeldReasonMuted || string(h.ID) != "agent:"+c.machine+"/"+session {
					t.Fatalf("held %d is %s %s", i, h.ID, h.Why)
				}
			}
			v.Epoch, v.Rev, v.AsOf, v.Build = "FX2NBPZ3LR2Q4MYGO4TUQRLNWA", 1<<40, time.Now().UnixMilli(), strings.Repeat("9", 80)
			b, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			if len(b) > assistantwire.CountsViewMaxBytes {
				t.Fatalf("the view takes %d bytes, past %d", len(b), assistantwire.CountsViewMaxBytes)
			}
			// A list cut by its bytes stops near them: the next row would not have fit.
			if c.keys < 0 && len(b) < assistantwire.CountsViewMaxBytes-viewRoom-1000 {
				t.Errorf("the view stopped at %d keys with %d bytes, far from the bound", len(count.Keys), len(b))
			}
		})
	}
}

// escapes is never less than what encoding/json adds to a string, and exactly that for the ids Persona makes (a byte
// that is not UTF-8 it counts at the most any Go release adds for it).
func TestEscapesBoundWhatJSONAdds(t *testing.T) {
	added := func(s string) int {
		b, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		return len(b) - 2 - len(s)
	}
	for b := range 256 {
		if s := string([]byte{byte(b)}); escapes(s) < added(s) {
			t.Errorf("byte %#x: escapes says %d, encoding/json adds %d", b, escapes(s), added(s))
		}
	}
	for _, s := range []string{"", "studio", "0b8f6f2c-5d1e-4a8e-9c1f-000000000001", "a\"b\\c\n", "<a&b>", "  ",
		"\xff\xfe", "é€😀", "\x01"} {
		if escapes(s) < added(s) {
			t.Errorf("%q: escapes says %d, encoding/json adds %d", s, escapes(s), added(s))
		}
	}
	for _, s := range []string{"studio", "0b8f6f2c-5d1e-4a8e-9c1f-000000000001", "a\"b\\c\n", "<a&b>", " ", "é€😀"} {
		if escapes(s) != added(s) {
			t.Errorf("%q: escapes says %d, encoding/json adds %d", s, escapes(s), added(s))
		}
	}
}

// View renders only the Agents list's tally of its own fleet: another surface's, or one with places the fleet does not
// hold, is a defect.
func TestViewRefusesAnotherTally(t *testing.T) {
	small, big := waitingFleet(t, "studio", 1, 0, shortIDs), waitingFleet(t, "studio", 3, 0, shortIDs)
	for _, c := range []struct {
		want string
		do   func()
	}{
		{"fleetcountadapt.Fleet.View: a tally of another surface than the Agents list's", func() {
			small.View(small.decide(fleetcount.SurfaceLink()), opt.None[assistantwire.TodoCounts]())
		}},
		{"fleetcountadapt.Fleet.View: a tally of another surface than the Agents list's", func() {
			var none fleetcount.Tally
			small.View(none, opt.None[assistantwire.TodoCounts]())
		}},
		{"fleetcountadapt.Fleet.View: a tally of another fleet", func() {
			small.View(big.decide(fleetcount.SurfaceBadge()), opt.None[assistantwire.TodoCounts]())
		}},
	} {
		func() {
			defer func() {
				if d, ok := recover().(defect.Defect); !ok || d.Error() != c.want {
					t.Errorf("raised %v, want the defect %q", d, c.want)
				}
			}()
			c.do()
		}()
	}
}

// The counts View writes are the counts the surface table (spec/fixtures/counts/surfaces.json) says it produces, and
// every producer the table names in this package is a function this test can run: a count View writes that the table
// does not list, or lists under another producer, fails, and so does a count the table says View writes that it does
// not.
func TestViewWritesTheCountsThatNameIt(t *testing.T) {
	// The code map: each producer in this package, by its table symbol, and the counts it writes.
	written := func(v assistantwire.CountsView) []string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		var ids struct {
			Counts map[string]json.RawMessage `json:"counts"`
			Totals map[string]json.RawMessage `json:"totals"`
		}
		if err := json.Unmarshal(b, &ids); err != nil {
			t.Fatal(err)
		}
		var out []string
		for id := range ids.Counts {
			if _, ok := ids.Totals[id]; !ok {
				t.Errorf("View writes %s without its total", id)
			}
			out = append(out, id)
		}
		sort.Strings(out)
		return out
	}
	f := waitingFleet(t, "studio", 1, 0, shortIDs)
	counted := opt.Some(assistantwire.TodoCounts{NeedsYou: 1})
	producers := map[string][]string{"Fleet.View": written(f.View(f.decide(fleetcount.SurfaceBadge()), counted))}
	b, err := os.ReadFile(surfacesTable)
	if err != nil {
		t.Fatal(err)
	}
	var table struct {
		Counts map[string]struct {
			Producer *struct {
				File   string `json:"file"`
				Symbol string `json:"symbol"`
			} `json:"producer"`
		} `json:"counts"`
	}
	if err := json.Unmarshal(b, &table); err != nil {
		t.Fatal(err)
	}
	named := map[string][]string{}
	for id, count := range table.Counts {
		if p := count.Producer; p != nil && path.Dir(p.File) == thisPackage {
			if _, ok := producers[p.Symbol]; !ok {
				t.Errorf("counts.%s: the table names %s in %s, which this test cannot run", id, p.Symbol, p.File)
			}
			named[p.Symbol] = append(named[p.Symbol], id)
		}
	}
	for symbol, ids := range producers {
		want := named[symbol]
		sort.Strings(want)
		if !slices.Equal(ids, want) {
			t.Errorf("%s writes %v; the table says it produces %v", symbol, ids, want)
		}
	}
}
