package db

import (
	"context"
	"testing"
	"time"
)

func TestModelRoutingTrialSummaries(t *testing.T) {
	d, ctx := open(t), context.Background()
	if _, err := d.pool.Exec(ctx, `DELETE FROM model_routing`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = d.pool.Exec(context.Background(), `DELETE FROM model_routing`) })
	now := time.Now().UTC().Truncate(time.Microsecond)
	since := now.AddDate(0, 0, -7)

	rows := []ModelRoutingRow{
		// Decision 1 (within 7d): active=perf (claude/m1); econ picks same (claude/m1) -> not a switch; alt picks codex/m2 -> switch.
		{At: now.Add(-2 * time.Hour), TodoID: "todo-trial-1", PlanID: "plan-1", TaskIndex: 0, SessionID: "trial-s-1", ProfileID: "perf", Active: true, Tier: "dev", Candidate: 0, Of: 2, Launcher: "claude", Model: "m1", Reason: "first"},
		{At: now.Add(-2 * time.Hour), TodoID: "todo-trial-1", PlanID: "plan-1", TaskIndex: 0, SessionID: "trial-s-1", ProfileID: "econ", Active: false, Tier: "dev", Candidate: 0, Of: 2, Launcher: "claude", Model: "m1", Reason: "first"},
		{At: now.Add(-2 * time.Hour), TodoID: "todo-trial-1", PlanID: "plan-1", TaskIndex: 0, SessionID: "trial-s-1", ProfileID: "alt", Active: false, Tier: "dev", Candidate: 1, Of: 2, Launcher: "codex", Model: "m2", Reason: "score"},
		// Duplicate trial row for (alt, trial-s-1): must still count as 1 distinct dispatch decision.
		{At: now.Add(-2*time.Hour + time.Second), TodoID: "todo-trial-1", PlanID: "plan-1", TaskIndex: 0, SessionID: "trial-s-1", ProfileID: "alt", Active: false, Tier: "dev", Candidate: 1, Of: 2, Launcher: "codex", Model: "m2", Reason: "score"},

		// Decision 2 (same todo, new session): active=perf (claude/m1); econ picks same launcher, different model (claude/m2) -> switch.
		{At: now.Add(-time.Hour), TodoID: "todo-trial-1", PlanID: "plan-1", TaskIndex: 0, SessionID: "trial-s-2", ProfileID: "perf", Active: true, Tier: "dev", Candidate: 0, Of: 2, Launcher: "claude", Model: "m1", Reason: "kept"},
		{At: now.Add(-time.Hour), TodoID: "todo-trial-1", PlanID: "plan-1", TaskIndex: 0, SessionID: "trial-s-2", ProfileID: "econ", Active: false, Tier: "dev", Candidate: 1, Of: 2, Launcher: "claude", Model: "m2", Reason: "score"},

		// Decision 3 (no session_id fallback, matched by todo/plan/task/at): active=perf (codex/m2); econ picks claude/m1 -> switch.
		{At: now.Add(-30 * time.Minute), TodoID: "todo-trial-3", PlanID: "plan-3", TaskIndex: 1, ProfileID: "perf", Active: true, Tier: "core", Candidate: 0, Of: 1, Launcher: "codex", Model: "m2", Reason: "only"},
		{At: now.Add(-30 * time.Minute), TodoID: "todo-trial-3", PlanID: "plan-3", TaskIndex: 1, ProfileID: "econ", Active: false, Tier: "core", Candidate: 0, Of: 1, Launcher: "claude", Model: "m1", Reason: "only"},

		// Decision 4 (orphan trial without an active row): counts in dispatches, not in would_switch.
		{At: now.Add(-10 * time.Minute), TodoID: "todo-trial-4", SessionID: "trial-s-orphan", ProfileID: "alt", Active: false, Tier: "dev", Candidate: 0, Of: 1, Launcher: "claude", Model: "m1", Reason: "only"},

		// Decision 5 (older than 7 days): must be excluded.
		{At: now.AddDate(0, 0, -8), TodoID: "todo-trial-old", SessionID: "trial-s-old", ProfileID: "perf", Active: true, Tier: "dev", Candidate: 0, Of: 1, Launcher: "claude", Model: "m1", Reason: "only"},
		{At: now.AddDate(0, 0, -8), TodoID: "todo-trial-old", SessionID: "trial-s-old", ProfileID: "econ", Active: false, Tier: "dev", Candidate: 0, Of: 1, Launcher: "codex", Model: "m2", Reason: "only"},
	}
	for _, r := range rows {
		if err := d.PutModelRouting(ctx, r); err != nil {
			t.Fatal(err)
		}
	}

	got, err := d.ModelRoutingTrialSummaries(ctx, since)
	if err != nil {
		t.Fatal(err)
	}
	want := []ModelRoutingTrialSummary{
		{ProfileID: "alt", Dispatches: 2, WouldSwitch: 1},
		{ProfileID: "econ", Dispatches: 3, WouldSwitch: 2},
	}
	if len(got) != len(want) {
		t.Fatalf("summaries = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("summaries[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}
