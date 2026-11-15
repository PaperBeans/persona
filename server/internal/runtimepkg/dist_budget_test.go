package runtimepkg

import (
	"os"
	"testing"
)

// TestDistBudget bounds what personad, persona-node and persona-manager embed of the runtime packages: every file
// Generate writes from integrations/. Measured 424,588 B on 2026-11-10 (the same files raw were 1,627,267 B); the
// budget was that +3%. Raised by 4,500 B on 2026-11-13 for the Paseo bridge's workspaces (#6295, #6297: 5,693 B over
// main's 434,235 B), and by 4,000 B more for the bridge reading the lines of forked Claude sessions (#6709). Raise it
// only for code a runtime needs, never to embed the packages raw again. The required whole-input clip pass (#2907) measured
// 445,466 B on untouched main and 447,388 B after its ordering, marker and linear-accounting fixes; the budget is the latter
// plus the same 334 B of headroom main had. Raised by 2,405 B for pi's ask_user that never hangs on an unseen dialog,
// an interrupt that waits for the turn to end, and the SDK's queued / settled / runtime.driven (#8097: 449,837 B over
// main's 447,432 B, with main's 290 B of headroom). Raised by 3,011 B for the Paseo bridge keeping cards and their
// answers across link drops, and the SDK's wait for the next node counted from the drop (#8128: 452,848 B over main's
// 449,837 B, with main's 290 B of headroom). Raised by 2,725 B for pi registering Persona's model providers in scope so a
// computer without a models.json runs pi (#8194: 455,573 B over main's 452,848 B, with main's 290 B of headroom).
func TestDistBudget(t *testing.T) {
	const budget = 455_863
	_, out := generateIntegrations(t)
	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	size := int64(0)
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().IsRegular() {
			size += info.Size()
		}
	}
	if size > budget {
		t.Fatalf("generated runtime packages are %d bytes, over the budget of %d", size, budget)
	}
	t.Logf("generated runtime packages: %d bytes (budget %d)", size, budget)
}
