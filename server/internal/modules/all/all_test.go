package all

import (
	"slices"
	"testing"

	"github.com/nwverse/persona/server/internal/assistant/kernel"
)

// The list has each module its build tags keep, and the kernel loads exactly that list. Run with -tags
// persona_core_only or persona_without_briefing as well (scripts/ci/server.sh).
func TestBuiltinFollowsTheBuildTags(t *testing.T) {
	var ids []string
	for _, m := range Builtin() {
		ids = append(ids, m.Info().ID)
	}
	if !slices.Equal(ids, wantModules) {
		t.Fatalf("built-in modules %v, want %v for this build's tags", ids, wantModules)
	}
	if got := kernel.Builtin(); len(got) != len(ids) {
		t.Fatalf("the kernel was provided %d modules, the list has %d", len(got), len(ids))
	}
	k := kernel.New(Builtin(), nil, t.Logf)
	for _, id := range wantModules {
		if !k.Loaded(id) {
			t.Errorf("%s is compiled in but does not load: %s", id, k.Skipped(id))
		}
	}
}
