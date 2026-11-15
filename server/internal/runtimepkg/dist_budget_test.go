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
// computer without a models.json runs pi (#8194: 455,573 B over main's 452,848 B, with main's 290 B of headroom). Raised
// by 1,337 B for the Paseo bridge's rows of closed and archived agents: Unread kept across an unload, archived rows'
// capabilities, the features last seen, efforts, its own words named for the hub (#8133: 456,910 B over main's 455,573 B,
// with main's 290 B of headroom). Raised by 2,149 B for the Paseo bridge's same-kind starts and the SDK's
// caps.same_kind (#8140: a launcher Paseo lacks runs on its provider of the same kind or is refused, an effort the
// model lacks goes down to one it offers, and the answer says both; 459,059 B over main's 456,910 B, with main's 290 B
// of headroom). Raised by 1,048 B for the Paseo bridge's agents on one Claude session (one id, the newer agent takes the
// messages) and its takeover of an archived agent with that agent's own provider (#8131: 460,362 B over main's 459,314 B,
// with main's 35 B of headroom). Raised by 484 B for the Paseo bridge answering Persona's own tools on every provider
// and marking Auto Accept launchers (#8141: 460,876 B over main's 460,392 B, with main's 5 B of headroom).
func TestDistBudget(t *testing.T) {
	const budget = 460_881
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
