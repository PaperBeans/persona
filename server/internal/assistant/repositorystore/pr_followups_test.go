package repositorystore

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nwverse/persona/server/internal/assistant/repository"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/db/dbtest"
)

func seedFollowupTodo(t testing.TB, st *Store, id string, doneAt time.Time) {
	t.Helper()
	if err := st.CreateTodo(context.Background(), db.TodoRow{
		ID:        id,
		Title:     "closed " + id,
		CreatedAt: doneAt,
		UpdatedAt: doneAt,
		DoneAt:    &doneAt,
		DoneBy:    db.DoneByReview,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPRFollowupWatchStatusPersistenceReloadAndRecovery(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	base := time.Date(2026, 11, 24, 6, 0, 0, 0, time.UTC)

	seedFollowupTodo(t, st, "todo_unwatched", base)
	seedFollowupTodo(t, st, "todo_active_fresh", base)
	seedFollowupTodo(t, st, "todo_active_stale", base.Add(-30*time.Hour))
	seedFollowupTodo(t, st, "todo_legacy", base.Add(-26*time.Hour))

	// 1. Upsert an initially unwatched subscription atomically with watch_status + watch_reason + watch_updated_at.
	if err := st.UpsertPRFollowupRecords(ctx, []repository.PRFollowup{{
		TodoID:         "todo_unwatched",
		PRURL:          "https://git.example.test/team/app/merge_requests/3",
		Supervisor:     "conv_sup",
		Origin:         "conv_origin",
		CreatedAt:      base,
		WatchStatus:    repository.PRFollowupWatchHostNotConnected,
		WatchReason:    repository.PRFollowupReasonUnsupportedHost,
		WatchUpdatedAt: &base,
	}}); err != nil {
		t.Fatal(err)
	}
	// Re-upserting the same (todo_id, pr_url) with active must keep the existing row untouched (ON CONFLICT DO NOTHING).
	if err := st.UpsertPRFollowups(ctx, "todo_unwatched", "conv_sup", "conv_origin",
		[]string{"https://git.example.test/team/app/merge_requests/3"}, base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	// 2. Upsert active subscriptions and simulate a legacy row whose watch_status is empty string.
	if err := st.UpsertPRFollowups(ctx, "todo_active_fresh", "conv_sup", "conv_origin",
		[]string{"https://github.com/o/r/pull/101"}, base); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertPRFollowups(ctx, "todo_active_stale", "conv_sup", "conv_origin",
		[]string{"https://github.com/o/r/pull/102"}, base.Add(-30*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertPRFollowups(ctx, "todo_legacy", "conv_sup", "conv_origin",
		[]string{"https://github.com/o/r/pull/103"}, base.Add(-26*time.Hour)); err != nil {
		t.Fatal(err)
	}
	// Explicitly clear watch_status on todo_legacy to match rows migrated from before watch_status existed.
	if err := st.Transaction(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE todo_pr_followups SET watch_status='', watch_reason='', watch_updated_at=NULL WHERE todo_id='todo_legacy'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	rows, err := st.ListPRFollowups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byTodo := map[string]repository.PRFollowup{}
	for _, r := range rows {
		byTodo[r.TodoID] = r
	}

	unwatched := byTodo["todo_unwatched"]
	if unwatched.WatchStatus != repository.PRFollowupWatchHostNotConnected ||
		unwatched.WatchReason != repository.PRFollowupReasonUnsupportedHost ||
		unwatched.LastObservedAt != nil ||
		unwatched.WatchUpdatedAt == nil || !unwatched.WatchUpdatedAt.Equal(base) {
		t.Fatalf("unwatched row = %+v", unwatched)
	}
	legacy := byTodo["todo_legacy"]
	if legacy.WatchStatus != "" || legacy.EffectiveWatchStatus() != repository.PRFollowupWatchActive || legacy.IsUnwatched() {
		t.Fatalf("legacy row = %+v", legacy)
	}

	// 3. Observe todo_active_fresh at base+5m so it is recently polled and active.
	obsFresh := base.Add(5 * time.Minute)
	fresh := byTodo["todo_active_fresh"]
	fresh.HeadOID = "h1"
	fresh.State = repository.ReviewOpen
	fresh.LastObservedAt = &obsFresh
	fresh.NotifiedConflictHead = "h1"
	updated, err := st.SavePRFollowupObservation(ctx, fresh)
	if err != nil || !updated {
		t.Fatalf("SavePRFollowupObservation fresh: updated=%v err=%v", updated, err)
	}

	// 4. StalePRFollowups at before=base-24h returns unwatched (even though created at base!), stale active, and stale legacy,
	//    but NOT the recently polled active row.
	stale, err := st.StalePRFollowups(ctx, base.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	staleIDs := map[string]bool{}
	for _, s := range stale {
		staleIDs[s.TodoID] = true
	}
	if !staleIDs["todo_unwatched"] || !staleIDs["todo_active_stale"] || !staleIDs["todo_legacy"] || staleIDs["todo_active_fresh"] {
		t.Fatalf("StalePRFollowups returned %+v", staleIDs)
	}

	// 5. Disconnect todo_active_fresh at base+10m: must preserve last_observed_at, head_oid, state, notified_conflict_head,
	//    and reject an older watch-status write (base+4m < obsFresh).
	if ok, err := st.SavePRFollowupWatchStatus(ctx, fresh.ID, repository.PRFollowupWatchHostNotConnected,
		repository.PRFollowupReasonHostNotConnected, base.Add(4*time.Minute)); err != nil || ok {
		t.Fatalf("stale watch status write should be ignored: ok=%v err=%v", ok, err)
	}
	disconnectAt := base.Add(10 * time.Minute)
	if ok, err := st.SavePRFollowupWatchStatus(ctx, fresh.ID, repository.PRFollowupWatchHostNotConnected,
		repository.PRFollowupReasonHostNotConnected, disconnectAt); err != nil || !ok {
		t.Fatalf("SavePRFollowupWatchStatus disconnect: ok=%v err=%v", ok, err)
	}

	// 6. Simulate a manager restart by opening a fresh Postgres connection to the same database.
	reloaded := openExisting(t, os.Getenv(dbtest.Env))
	reloadedRows, err := reloaded.ListPRFollowups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	reloadedByTodo := map[string]repository.PRFollowup{}
	for _, r := range reloadedRows {
		reloadedByTodo[r.TodoID] = r
	}
	gotFresh := reloadedByTodo["todo_active_fresh"]
	if gotFresh.WatchStatus != repository.PRFollowupWatchHostNotConnected ||
		gotFresh.WatchReason != repository.PRFollowupReasonHostNotConnected ||
		gotFresh.WatchUpdatedAt == nil || !gotFresh.WatchUpdatedAt.Equal(disconnectAt) ||
		gotFresh.LastObservedAt == nil || !gotFresh.LastObservedAt.Equal(obsFresh) ||
		gotFresh.HeadOID != "h1" || gotFresh.State != repository.ReviewOpen || gotFresh.NotifiedConflictHead != "h1" {
		t.Fatalf("reloaded disconnected row lost state: %+v", gotFresh)
	}
	gotUnwatched := reloadedByTodo["todo_unwatched"]
	if gotUnwatched.WatchStatus != repository.PRFollowupWatchHostNotConnected ||
		gotUnwatched.WatchReason != repository.PRFollowupReasonUnsupportedHost ||
		gotUnwatched.LastObservedAt != nil {
		t.Fatalf("reloaded unwatched row = %+v", gotUnwatched)
	}

	// 7. A stale observation older than disconnectAt must not clobber the newer disconnect.
	staleObs := base.Add(8 * time.Minute)
	gotFresh.LastObservedAt = &staleObs
	gotFresh.WatchStatus = repository.PRFollowupWatchActive
	if ok, err := reloaded.SavePRFollowupObservation(ctx, gotFresh); err != nil || ok {
		t.Fatalf("stale observation overwrote newer disconnect: ok=%v err=%v", ok, err)
	}

	// 8. A fresh observation after disconnect recovers watch_status back to active and clears watch_reason,
	//    even if the caller leaves WatchStatus at host_not_connected on the input struct.
	recoverAt := base.Add(15 * time.Minute)
	gotFresh.HeadOID = "h2"
	gotFresh.LastObservedAt = &recoverAt
	gotFresh.WatchStatus = repository.PRFollowupWatchHostNotConnected
	gotFresh.WatchReason = repository.PRFollowupReasonHostNotConnected
	gotFresh.WatchUpdatedAt = &recoverAt
	if ok, err := reloaded.SavePRFollowupObservation(ctx, gotFresh); err != nil || !ok {
		t.Fatalf("recovery SavePRFollowupObservation failed: ok=%v err=%v", ok, err)
	}

	afterRecover, err := reloaded.ListPRFollowups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range afterRecover {
		if r.TodoID != "todo_active_fresh" {
			continue
		}
		if r.WatchStatus != repository.PRFollowupWatchActive || r.WatchReason != "" ||
			r.WatchUpdatedAt == nil || !r.WatchUpdatedAt.Equal(recoverAt) ||
			r.LastObservedAt == nil || !r.LastObservedAt.Equal(recoverAt) || r.HeadOID != "h2" {
			t.Fatalf("recovered row = %+v", r)
		}
	}
}
