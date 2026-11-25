package fleetcountadapt

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/nwverse/persona/server/internal/assistant/fleetcount"
	"github.com/nwverse/persona/server/internal/assistantwire"
)

// viewSamples are the counts views of schema/wire/counts.schema.json the clients' round trips read
// (docs/protocol/counts.md, #15512).
const viewSamples = "../../../../../spec/fixtures/counts/view_samples.json"

// Each view sample that names a needs-you case holds, as its counts and totals, what View writes of fleetcount's
// Agents list tally of that case's sessions and cards: the rows and the number, each held row's reason a word the wire
// knows, and the totals repeating the number. The case hands in its quiet agents, which the view lists in neither
// list, as the live round never hands them in.
func TestViewSamplesAreFleetcountsRows(t *testing.T) {
	byName := map[string]needsYouCase{}
	for _, c := range needsYouCases(t) {
		byName[c.Name] = c
	}
	b, err := os.ReadFile(viewSamples)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Cases []struct {
			Name  string `json:"name"`
			Input struct {
				NeedsYou string `json:"needs_you"`
			} `json:"input"`
			Expected struct {
				View assistantwire.CountsView `json:"view"`
			} `json:"expected"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(b, &file); err != nil {
		t.Fatalf("%s: %v", viewSamples, err)
	}
	checked := 0
	for _, s := range file.Cases {
		if s.Input.NeedsYou == "" {
			continue // a sample of the shape alone (more, an absent count), which no fixture case gives
		}
		t.Run(s.Name, func(t *testing.T) {
			c, ok := byName[s.Input.NeedsYou]
			if !ok {
				t.Fatalf("no needs-you case %q", s.Input.NeedsYou)
			}
			want := s.Expected.View.Counts.AgentsNeedsYou
			if want == nil {
				t.Fatal("no agents_needs_you")
			}
			f := c.fleet(t)
			got := f.View(f.decide(fleetcount.SurfaceBadge()))
			// A sample may leave its totals out (they are optional); the manager always writes them.
			if tot := s.Expected.View.Totals; !reflect.DeepEqual(got.Counts, s.Expected.View.Counts) ||
				tot != nil && !reflect.DeepEqual(got.Totals, tot) {
				g, _ := json.Marshal(got)
				w, _ := json.Marshal(s.Expected.View)
				t.Fatalf("what View writes\n %s\nthe sample's\n %s", g, w)
			}
			for _, h := range want.Held {
				if !h.Why.Valid() {
					t.Errorf("%s: held as %q, a word the wire does not know", h.ID, h.Why)
				}
			}
			if tot := got.Totals; tot == nil || tot.AgentsNeedsYou == nil || *tot.AgentsNeedsYou != want.N {
				t.Errorf("totals %v, want agents_needs_you %d", tot, want.N)
			}
		})
		checked++
	}
	if checked == 0 {
		t.Fatalf("%s: no sample names a needs-you case", viewSamples)
	}
}
