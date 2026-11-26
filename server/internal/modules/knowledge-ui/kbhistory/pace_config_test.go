package kbhistory

import (
	"encoding/json"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

// The paces kb.history's profiles and overrides in manager.json give (the assistant's TestHistoryConfigFromManagerJSON
// reads the file): a profile's defaults with the overrides over them, and slow_factor stretching every interval and
// rest. Moved here with the import (#14407).
func TestPaceFromManagerJSON(t *testing.T) {
	var h assistantwire.KBHistory
	raw := `{"enabled":true,"sources":{"zeta":{"profile":"standard","needs_search_index":false,"interval_seconds":[4,9]},"alpha":{"page_limit":5,"quiet_hours":2}}}`
	if err := json.Unmarshal([]byte(raw), &h); err != nil {
		t.Fatal(err)
	}
	z, a := PaceFor(h.Sources[0]), PaceFor(h.Sources[1])
	if z.Profile != "standard" || z.Interval != (assistantwire.KBRange{4, 9}) || h.Sources[0].SearchIndexNeeded() {
		t.Fatalf("zeta %+v", z)
	}
	if a.Profile != "careful" || a.PageLimit != 5 || a.QuietHours != (assistantwire.KBRange{2, 2}) || !h.Sources[1].SearchIndexNeeded() {
		t.Fatalf("alpha %+v", a)
	}
	// slow_factor stretches every interval and rest.
	var two assistantwire.KBHistory
	if err := json.Unmarshal([]byte(`{"sources":{"x":{"profile":"standard","slow_factor":2}}}`), &two); err != nil {
		t.Fatal(err)
	}
	if p := PaceFor(two.Sources[0]); p.Interval != (assistantwire.KBRange{6, 16}) || p.Rest != (assistantwire.KBRange{4, 20}) {
		t.Fatalf("slow factor %+v", p)
	}
	if Indexed(nil) { // not open: nothing says it has one (was in the assistant's TestHistoryWaitsForTheSearchIndexTheLaneBuilds)
		t.Fatal("no knowledge base, yet indexed")
	}
}
