package assistant

import (
	"context"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

func TestSupervisePendingReviewNudgeTodosV2(t *testing.T) {
	s, f, todo := newSupervisedService(t)
	quietDigest(s)
	holdDigest(s)
	ctx := context.Background()

	waitsForReview(t, s, f, todo.ID, "Finished implementation and tests.")
	cur := todoNow(s, todo.ID)
	if cur.Supervision == nil || cur.Supervision.State != reviewPending || cur.Supervision.PendingSince == nil {
		t.Fatalf("expected pending_review with PendingSince, got %+v", cur.Supervision)
	}
	since := *cur.Supervision.PendingSince

	projectAt := func(at time.Time) (assistantwire.TodoSummary, assistantwire.TodoCounts) {
		t.Helper()
		list, err := s.listedTodos(ctx, false)
		if err != nil {
			t.Fatal(err)
		}
		p := s.todoProjectionOf(ctx, list, at)
		return p.byID()[todo.ID], p.counts()
	}

	// todos_v2 == false (default): no review_nudge even past 2h, and legacy projection stays just_finished.
	legacyView, legacyCounts := projectAt(since.Add(3 * time.Hour))
	if legacyView.Section != assistantwire.TodoListSectionJustFinished || legacyCounts.NeedsYou != 0 {
		t.Fatalf("legacy projection = %+v counts=%+v", legacyView, legacyCounts)
	}
	s.superviseWatch(ctx, since.Add(3*time.Hour))
	if slices.Contains(pendingKinds(s, todo.ID), sigReviewNudge) {
		t.Fatalf("todos_v2=false emitted %s: %v", sigReviewNudge, pendingKinds(s, todo.ID))
	}

	// Enable todos_v2 via the settings endpoint.
	if w, _ := permissionSettingsRequest(t, s, "PUT", "/api/v1/assistant", s.managerIdentity(), "full", `{"todos_v2":true,"expected_todos_v2":false}`); w.Code != http.StatusOK {
		t.Fatalf("enable todos_v2: code=%d body=%s", w.Code, w.Body.String())
	}

	// < 2h: stays in working, no nudge yet.
	v1h, c1h := projectAt(since.Add(90 * time.Minute))
	if v1h.Section != assistantwire.TodoListSectionWorking || c1h.NeedsYou != 0 {
		t.Fatalf("todos_v2 <2h: view=%+v counts=%+v", v1h, c1h)
	}
	if !v1h.NextChangeAt.Equal(since.Add(pendingReviewEscalateAfter).UTC()) {
		t.Fatalf("todos_v2 <2h: next_change_at=%v, want %v", v1h.NextChangeAt, since.Add(pendingReviewEscalateAfter).UTC())
	}
	s.superviseWatch(ctx, since.Add(pendingReviewNudgeAfter-time.Minute))
	if slices.Contains(pendingKinds(s, todo.ID), sigReviewNudge) {
		t.Fatalf("todos_v2 <2h emitted %s early: %v", sigReviewNudge, pendingKinds(s, todo.ID))
	}

	// 2h..24h: emits one lightweight supervisor nudge (deduplicated per round) while staying in working.
	beforeNotices := len(f.notices())
	s.superviseWatch(ctx, since.Add(pendingReviewNudgeAfter+time.Minute))
	if !slices.Contains(pendingKinds(s, todo.ID), sigReviewNudge) {
		t.Fatalf("todos_v2 >2h did not queue %s: %v", sigReviewNudge, pendingKinds(s, todo.ID))
	}
	setSuperviseTimings(s, shortSuperviseTimings)
	releaseDigest(s)
	waitFor(t, "review_nudge supervisor digest", func() bool { return len(f.notices()) > beforeNotices })
	notice := f.notices()[beforeNotices]
	if !strings.Contains(notice, "signal=review_nudge") {
		t.Fatalf("expected signal=review_nudge in supervisor notice:\n%s", notice)
	}

	// Repeated ticks in 2h..24h must not re-nudge for the same pending_review round.
	holdDigest(s)
	s.superviseWatch(ctx, since.Add(4*time.Hour))
	s.superviseWatch(ctx, since.Add(12*time.Hour))
	if slices.Contains(pendingKinds(s, todo.ID), sigReviewNudge) {
		t.Fatalf("duplicate %s queued in same round: %v", sigReviewNudge, pendingKinds(s, todo.ID))
	}

	// > 24h: forced escalation into needs_you, and no further review_nudge.
	v25h, c25h := projectAt(since.Add(25 * time.Hour))
	if v25h.Section != assistantwire.TodoListSectionNeedsYou || c25h.Sections[assistantwire.TodoListSectionNeedsYou] != 1 {
		t.Fatalf("todos_v2 >24h: view=%+v counts=%+v, want needs_you", v25h, c25h)
	}
	s.superviseWatch(ctx, since.Add(25*time.Hour))
	if slices.Contains(pendingKinds(s, todo.ID), sigReviewNudge) {
		t.Fatalf("todos_v2 >24h should not queue %s after escalation: %v", sigReviewNudge, pendingKinds(s, todo.ID))
	}

	// Turning todos_v2 back off immediately restores the exact legacy projection.
	if w, _ := permissionSettingsRequest(t, s, "PUT", "/api/v1/assistant", s.managerIdentity(), "full", `{"todos_v2":false,"expected_todos_v2":true}`); w.Code != http.StatusOK {
		t.Fatalf("disable todos_v2: code=%d body=%s", w.Code, w.Body.String())
	}
	afterRollback, rollbackCounts := projectAt(since.Add(3 * time.Hour))
	if !reflect.DeepEqual(afterRollback, legacyView) || !reflect.DeepEqual(rollbackCounts, legacyCounts) {
		t.Fatalf("rollback mismatch:\ngot:  %+v %+v\nwant: %+v %+v", afterRollback, rollbackCounts, legacyView, legacyCounts)
	}
}
