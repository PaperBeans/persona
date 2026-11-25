package projection

import (
	"reflect"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestProjectionPendingReviewSLAGatedByTodosV2(t *testing.T) {
	since := testNow
	base := Facts{
		ID:        "todo_pr_sla",
		Title:     "Wait for supervisor review",
		CreatedAt: since.Add(-time.Hour),
		UpdatedAt: since,
		Origin:    "conv_main",
		Agents:    []Agent{{MachineID: "m1", SessionID: "s1", Title: "Worker"}},
		Sessions:  []proto.Session{{MachineID: "m1", SessionID: "s1", State: proto.StateIdle, StateSince: since, UpdatedAt: since}},
		Supervision: &Supervision{
			Supervisor:   "conv_lead",
			State:        reviewPending,
			PendingSince: at(since),
			UpdatedAt:    since,
		},
	}

	// todos_v2 == false: legacy behavior is preserved at <2h, 2h..24h, and >24h.
	legacy1h := View(base, since.Add(time.Hour))
	legacy12h := View(base, since.Add(12*time.Hour))
	legacy25h := View(base, since.Add(25*time.Hour))
	for name, v := range map[string]assistantwire.TodoSummary{"1h": legacy1h, "12h": legacy12h, "25h": legacy25h} {
		if v.Phase != assistantwire.TodoPhaseReady || v.Section != assistantwire.TodoListSectionJustFinished || v.Reason != assistantwire.TodoReasonFinishedUnseen {
			t.Fatalf("legacy %s: got phase=%q section=%q reason=%q, want ready/just_finished/finished_unseen", name, v.Phase, v.Section, v.Reason)
		}
		if !v.NextChangeAt.IsZero() {
			t.Fatalf("legacy %s: unexpected next_change_at=%v", name, v.NextChangeAt)
		}
	}

	// todos_v2 == true: stays in working for <= 24h (with next_change_at at +24h), then escalates to needs_you.
	v2 := base
	v2.TodosV2 = true
	wantEscalateAt := since.Add(24 * time.Hour).UTC()

	for _, tc := range []struct {
		name string
		at   time.Time
	}{
		{"<2h", since.Add(time.Hour)},
		{"2h..24h", since.Add(12 * time.Hour)},
		{"just_before_24h", since.Add(24*time.Hour - time.Second)},
	} {
		v := View(v2, tc.at)
		if v.Phase != assistantwire.TodoPhaseWorking || v.Section != assistantwire.TodoListSectionWorking || v.Reason != assistantwire.TodoReasonPersonaWorking {
			t.Fatalf("todos_v2 %s: got phase=%q section=%q reason=%q, want working/working/persona_working", tc.name, v.Phase, v.Section, v.Reason)
		}
		if !v.NextChangeAt.Equal(wantEscalateAt) {
			t.Fatalf("todos_v2 %s: next_change_at=%v, want %v", tc.name, v.NextChangeAt, wantEscalateAt)
		}
		counts := Counts([]Facts{v2}, []assistantwire.TodoSummary{v})
		if counts.NeedsYou != 0 || counts.InProgress != 1 {
			t.Fatalf("todos_v2 %s: counts=%+v, want needs_you=0 in_progress=1", tc.name, counts)
		}
	}

	for _, tc := range []struct {
		name string
		at   time.Time
	}{
		{"at_24h", since.Add(24 * time.Hour)},
		{"after_24h", since.Add(25 * time.Hour)},
	} {
		v := View(v2, tc.at)
		if v.Phase != assistantwire.TodoPhaseNeedsYou || v.Section != assistantwire.TodoListSectionNeedsYou || v.Reason != assistantwire.TodoReasonOwnerDecides {
			t.Fatalf("todos_v2 %s: got phase=%q section=%q reason=%q, want needs_you/needs_you/owner_decides", tc.name, v.Phase, v.Section, v.Reason)
		}
		counts := Counts([]Facts{v2}, []assistantwire.TodoSummary{v})
		if counts.NeedsYou != 1 || counts.InProgress != 0 {
			t.Fatalf("todos_v2 %s: counts=%+v, want needs_you=1 in_progress=0", tc.name, counts)
		}
	}

	// Turning todos_v2 back off restores the exact legacy view without data loss.
	v2.TodosV2 = false
	if got := View(v2, since.Add(25*time.Hour)); !reflect.DeepEqual(got, legacy25h) {
		t.Fatalf("rollback mismatch:\ngot:  %+v\nwant: %+v", got, legacy25h)
	}
}

func TestProjectionCanWaitAgingGatedByTodosV2(t *testing.T) {
	askedAt := testNow
	base := Facts{
		ID:        "todo_can_wait_sla",
		Title:     "Pick rollout window",
		CreatedAt: askedAt.Add(-time.Hour),
		UpdatedAt: askedAt,
		Origin:    "conv_main",
		Ask: &Ask{
			Outcome:     outcomeDecide,
			Verdict:     reviewNeedsUser,
			AskedAt:     askedAt,
			Question:    "Ship today or tomorrow?",
			Recommend:   "Tomorrow",
			ForBriefing: true,
			Options: []assistantwire.OwnerAskOption{
				{Label: "Today", LeadsTo: "Faster"},
				{Label: "Tomorrow", LeadsTo: "Safer"},
			},
		},
	}

	// todos_v2 == false: stays in can_wait even past 48h, with no aging next_change_at.
	legacy72h := View(base, askedAt.Add(72*time.Hour))
	if legacy72h.Section != assistantwire.TodoListSectionCanWait || !legacy72h.CanWait || !legacy72h.NextChangeAt.IsZero() {
		t.Fatalf("legacy 72h: got section=%q can_wait=%t next_change_at=%v", legacy72h.Section, legacy72h.CanWait, legacy72h.NextChangeAt)
	}
	if counts := Counts([]Facts{base}, []assistantwire.TodoSummary{legacy72h}); counts.NeedsYou != 0 {
		t.Fatalf("legacy 72h: needs_you=%d, want 0", counts.NeedsYou)
	}

	// todos_v2 == true: stays in can_wait <48h with next_change_at at +48h, then ages into active needs_you.
	v2 := base
	v2.TodosV2 = true
	wantAgeAt := askedAt.Add(48 * time.Hour).UTC()

	for _, tc := range []struct {
		name string
		at   time.Time
	}{
		{"12h", askedAt.Add(12 * time.Hour)},
		{"just_before_48h", askedAt.Add(48*time.Hour - time.Second)},
	} {
		v := View(v2, tc.at)
		if v.Phase != assistantwire.TodoPhaseNeedsYou || v.Section != assistantwire.TodoListSectionCanWait || !v.CanWait {
			t.Fatalf("todos_v2 %s: got phase=%q section=%q can_wait=%t, want needs_you/can_wait/true", tc.name, v.Phase, v.Section, v.CanWait)
		}
		if len(v.Asks) != 1 || !v.Asks[0].CanWait {
			t.Fatalf("todos_v2 %s: asks=%+v, want single can_wait ask", tc.name, v.Asks)
		}
		if !v.NextChangeAt.Equal(wantAgeAt) {
			t.Fatalf("todos_v2 %s: next_change_at=%v, want %v", tc.name, v.NextChangeAt, wantAgeAt)
		}
		if counts := Counts([]Facts{v2}, []assistantwire.TodoSummary{v}); counts.NeedsYou != 0 {
			t.Fatalf("todos_v2 %s: needs_you=%d, want 0", tc.name, counts.NeedsYou)
		}
	}

	for _, tc := range []struct {
		name string
		at   time.Time
	}{
		{"at_48h", askedAt.Add(48 * time.Hour)},
		{"after_48h", askedAt.Add(49 * time.Hour)},
	} {
		v := View(v2, tc.at)
		if v.Phase != assistantwire.TodoPhaseNeedsYou || v.Section != assistantwire.TodoListSectionNeedsYou || v.Reason != assistantwire.TodoReasonOwnerDecides || v.CanWait {
			t.Fatalf("todos_v2 %s: got phase=%q section=%q reason=%q can_wait=%t, want needs_you/needs_you/owner_decides/false", tc.name, v.Phase, v.Section, v.Reason, v.CanWait)
		}
		if len(v.Asks) != 1 || v.Asks[0].CanWait {
			t.Fatalf("todos_v2 %s: asks=%+v, want single active ask with can_wait=false", tc.name, v.Asks)
		}
		if counts := Counts([]Facts{v2}, []assistantwire.TodoSummary{v}); counts.NeedsYou != 1 {
			t.Fatalf("todos_v2 %s: needs_you=%d, want 1", tc.name, counts.NeedsYou)
		}
	}

	// Rollback restores legacy can_wait identically.
	v2.TodosV2 = false
	if got := View(v2, askedAt.Add(72*time.Hour)); !reflect.DeepEqual(got, legacy72h) {
		t.Fatalf("rollback mismatch:\ngot:  %+v\nwant: %+v", got, legacy72h)
	}
}
