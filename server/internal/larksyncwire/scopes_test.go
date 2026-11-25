package larksyncwire

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
)

// The fixture records the official API requirements, independently of the requested packs. In particular,
// field permissions and similarly named list/get permissions must not stand in for API access.
func TestScopePacksCoverSyncReaders(t *testing.T) {
	raw, err := os.ReadFile("testdata/sync_scope_requirements.json")
	if err != nil {
		t.Fatal(err)
	}
	var requirements []struct {
		Reader   string   `json:"reader"`
		URL      string   `json:"url"`
		AnyOf    []string `json:"any_of"`
		FullOnly bool     `json:"full_only"`
	}
	if err := json.Unmarshal(raw, &requirements); err != nil {
		t.Fatal(err)
	}
	for _, tier := range []string{TierDocs, TierFull} {
		scopes := ScopesFor(tier)
		seen := map[string]bool{}
		for _, scope := range scopes {
			if seen[scope] {
				t.Errorf("%s requests duplicate scope %s", tier, scope)
			}
			seen[scope] = true
		}
		for _, required := range requirements {
			if required.FullOnly && tier != TierFull {
				continue
			}
			if !slices.ContainsFunc(required.AnyOf, func(scope string) bool { return slices.Contains(scopes, scope) }) {
				t.Errorf("%s: %s requires one of %v (%s)", tier, required.Reader, required.AnyOf, required.URL)
			}
		}
	}
}
