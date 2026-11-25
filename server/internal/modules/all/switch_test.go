package all

import (
	"testing"

	"github.com/nwverse/persona/server/internal/moduleswitch/switchtest"
)

// Every provider seam a module of this build fills follows the run-time module switch (docs/architecture/
// core-and-modules.md, "The switch"): present with the module on, absent with it switched off, exactly as in a build
// without it. A new seam that ignores the switch fails here. Run with each persona_without_<id> tag as well.
func TestEverySeamFollowsTheSwitch(t *testing.T) {
	switchtest.Check(t, ".", wantModules, []string{"kernel.Provide"}, nil)
}
