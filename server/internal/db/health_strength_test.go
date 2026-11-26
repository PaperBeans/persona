package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"
)

func strengthID(n int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", n) }
func strengthFixture(t *testing.T) (*DB, context.Context, StrengthExercise, StrengthSet) {
	t.Helper()
	d, ctx := open(t), context.Background()
	e, _, err := d.HealthStrengthExerciseSave(ctx, StrengthExercise{ID: strengthID(1), Name: "Press", WeightRule: "stack"})
	if err != nil {
		t.Fatal(err)
	}
	v := StrengthSet{ID: strengthID(2), ExerciseID: e.ID, Weight: 40, Reps: 10, At: time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)}
	return d, ctx, e, v
}
func TestHealthStrengthSetsIdempotencyUndoAndDelete(t *testing.T) {
	d, ctx, e, v := strengthFixture(t)
	first, err := d.HealthStrengthSetAdd(ctx, []StrengthSet{v})
	if err != nil {
		t.Fatal(err)
	}
	v.Weight = 80
	again, err := d.HealthStrengthSetAdd(ctx, []StrengthSet{v})
	if err != nil || again[0].Weight != 40 || again[0].Seq != first[0].Seq {
		t.Fatalf("retry: %+v %v", again, err)
	}
	undo, changed, err := d.HealthStrengthSetUndo(ctx, v.ID)
	if err != nil || !changed || undo.DeletedAt == nil || undo.Seq <= first[0].Seq {
		t.Fatalf("undo: %+v %v", undo, err)
	}
	retry, changed, err := d.HealthStrengthSetUndo(ctx, v.ID)
	if err != nil || changed || retry.Seq != undo.Seq {
		t.Fatal("repeated undo changed seq", err)
	}
	again, err = d.HealthStrengthSetAdd(ctx, []StrengthSet{v})
	if err != nil || again[0].DeletedAt == nil {
		t.Fatal("retry revived tombstone", err)
	}
	v.ID = strengthID(3)
	if _, err = d.HealthStrengthSetAdd(ctx, []StrengthSet{v}); err != nil {
		t.Fatal(err)
	}
	if changed, err = d.HealthStrengthExerciseDelete(ctx, e.ID); err != nil || !changed {
		t.Fatal(err)
	}
	rows, err := d.HealthStrengthSets(ctx, StrengthSetFilter{})
	if err != nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
	changes, err := d.HealthStrengthSetChanges(ctx, first[0].Seq, 10)
	if err != nil || len(changes) != 2 || changes[1].DeletedAt == nil {
		t.Fatal(changes, err)
	}
	if _, err = d.HealthStrengthSetAdd(ctx, []StrengthSet{{ID: strengthID(4), ExerciseID: e.ID, Reps: 1, At: v.At}}); err == nil {
		t.Fatal("logged a deleted exercise")
	}
}
func TestHealthStrengthWorkoutWindowsBothOrders(t *testing.T) {
	d, ctx, _, v := strengthFixture(t)
	start := v.At
	for i, delta := range []time.Duration{-31 * time.Minute, -30 * time.Minute, 4 * time.Hour, 4*time.Hour + time.Minute} {
		s := v
		s.ID = strengthID(10 + i)
		s.At = start.Add(delta)
		if _, err := d.HealthStrengthSetAdd(ctx, []StrengthSet{s}); err != nil {
			t.Fatal(err)
		}
	}
	w := StrengthWorkout{ID: strengthID(30), Start: start, End: start.Add(time.Hour), Report: json.RawMessage(`{}`)}
	n, err := d.HealthStrengthWorkoutAdd(ctx, w)
	if err != nil || n != 2 {
		t.Fatal(n, err)
	}
	w.End = w.End.Add(24 * time.Hour)
	n, err = d.HealthStrengthWorkoutAdd(ctx, w)
	if err != nil || n != 0 {
		t.Fatal("retry changed window", n, err)
	}
	v.At = start.Add(3 * time.Hour)
	got, err := d.HealthStrengthSetAdd(ctx, []StrengthSet{v})
	if err != nil || got[0].WorkoutID == nil || *got[0].WorkoutID != w.ID {
		t.Fatal(got, err)
	}
	w.ID = strengthID(31)
	w.Start = start.Add(2 * time.Hour)
	w.End = w.Start.Add(time.Hour)
	if _, err = d.HealthStrengthWorkoutAdd(ctx, w); err != nil {
		t.Fatal(err)
	}
	v.ID = strengthID(40)
	got, err = d.HealthStrengthSetAdd(ctx, []StrengthSet{v})
	if err != nil || got[0].WorkoutID == nil || *got[0].WorkoutID != w.ID {
		t.Fatal("latest eligible window", got, err)
	}
	old, err := d.HealthStrengthSets(ctx, StrengthSetFilter{Workout: strengthID(30)})
	if err != nil || len(old) != 3 {
		t.Fatal("stole linked sets", old, err)
	}
}
func strengthDraftFixture(t *testing.T, d *DB, ctx context.Context, n int) (StrengthDraft, StrengthCard) {
	t.Helper()
	at := time.Now().UTC()
	c := StrengthCard{ID: "health-strength-" + strengthID(n), Conversation: "training", MachineID: "computer", FeedID: fmt.Sprint("feed", n), View: json.RawMessage(`{"title":"Draft"}`), Version: 1, At: at}
	v := StrengthDraft{ID: strengthID(n), Kind: "sets", Body: json.RawMessage(`{"sets":[]}`), Conversation: c.Conversation, MachineID: c.MachineID, CardID: c.ID, CardVersion: 1}
	if err := d.HealthStrengthDraftCreate(ctx, v, c); err != nil {
		t.Fatal(err)
	}
	return v, c
}
func TestHealthStrengthDraftTransactions(t *testing.T) {
	d, ctx, _, set := strengthFixture(t)
	v, c := strengthDraftFixture(t, d, ctx, 50)
	edited, card, err := d.HealthStrengthDraftEdit(ctx, v.ID, 1, json.RawMessage(`{"sets":[1]}`), json.RawMessage(`{"title":"Edited"}`), c.At.Add(time.Second))
	if err != nil || edited.Revision != 2 || card.Version != 2 {
		t.Fatal(edited, card, err)
	}
	if _, _, err = d.HealthStrengthDraftEdit(ctx, v.ID, 1, v.Body, c.View, c.At); !errors.Is(err, ErrHealthStrengthDraftStale) {
		t.Fatal(err)
	}
	if _, err = d.UpdateAssistantView(ctx, c.Conversation, c.ID, 2, c.View, c.At); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = d.HealthStrengthDraftConfirm(ctx, v.ID, 2, []StrengthSet{set}, v.Body, c.View, c.At); !errors.Is(err, ErrHealthStrengthDraftStale) {
		t.Fatal(err)
	}
	rows, _ := d.HealthStrengthSetChanges(ctx, 0, 100)
	if len(rows) != 0 {
		t.Fatal("partial confirm leaked sets")
	}
	v, c = strengthDraftFixture(t, d, ctx, 51)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Go(func() {
			_, _, _, err := d.HealthStrengthDraftConfirm(ctx, v.ID, 1, []StrengthSet{set}, json.RawMessage(`{"settled":{"client":"one"}}`), c.View, time.Now())
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	successes, settled := 0, 0
	for err := range errs {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrHealthStrengthDraftSettled) {
			settled++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || settled != 1 {
		t.Fatal(successes, settled)
	}
	if _, _, err = d.HealthStrengthDraftEdit(ctx, v.ID, 1, v.Body, c.View, c.At); !errors.Is(err, ErrHealthStrengthDraftSettled) {
		t.Fatal(err)
	}
	v, c = strengthDraftFixture(t, d, ctx, 52)
	before, _ := d.HealthStrengthSetChanges(ctx, 0, 100)
	if _, _, err = d.HealthStrengthDraftDiscard(ctx, v.ID, 1, v.Body, c.View, c.At); err != nil {
		t.Fatal(err)
	}
	after, _ := d.HealthStrengthSetChanges(ctx, 0, 100)
	if len(after) != len(before) {
		t.Fatal("discard touched confirmed stream")
	}
	v, c = strengthDraftFixture(t, d, ctx, 53)
	next := c
	next.ID += "-2"
	next.FeedID += "-2"
	next.Conversation = "new-training"
	moved, err := d.HealthStrengthDraftMove(ctx, v.ID, 1, json.RawMessage(`{"title":"Moved"}`), next)
	if err != nil || moved.CardID != next.ID || moved.Revision != 2 {
		t.Fatal(moved, err)
	}
	old, err := d.AssistantView(ctx, c.Conversation, c.ID)
	if err != nil || old.Version != 2 {
		t.Fatal(old, err)
	}
}
func TestHealthStrengthConstraintsAndOwnership(t *testing.T) {
	d, ctx, e, v := strengthFixture(t)
	for _, table := range []string{"health_strength_sets", "health_strength_drafts", "health_strength_workouts", "health_strength_exercises"} {
		if !slices.Contains(ManagerTables(), table) || slices.Contains(HubTables(), table) {
			t.Fatal(table)
		}
	}
	var owner string
	if err := d.pool.QueryRow(ctx, `SELECT pg_get_serial_sequence('health_strength_sets','seq')`).Scan(&owner); err != nil || owner != "public.health_strength_set_seq" {
		t.Fatal(owner, err)
	}
	for _, bad := range []StrengthSet{{ID: v.ID, ExerciseID: e.ID, Weight: -1, Reps: 1, At: v.At}, {ID: v.ID, ExerciseID: e.ID, Weight: 1, Reps: 0, At: v.At}} {
		if _, err := d.HealthStrengthSetAdd(ctx, []StrengthSet{bad}); err == nil {
			t.Fatal("accepted invalid set")
		}
	}
	e.ID = strengthID(90)
	e.WeightRule = "invalid"
	if _, _, err := d.HealthStrengthExerciseSave(ctx, e); err == nil {
		t.Fatal("accepted invalid weight rule")
	}
	e.WeightRule = "stack"
	if _, _, err := d.HealthStrengthExerciseSave(ctx, e); !errors.Is(err, ErrHealthStrengthName) {
		t.Fatal("name conflict", err)
	}
	if _, err := d.pool.Exec(ctx, `INSERT INTO health_strength_drafts(id,kind,body,conversation,machine_id,card_id,card_version,created_at,updated_at) VALUES($1,'bad','{}','x','x','x',1,now(),now())`, strengthID(91)); err == nil {
		t.Fatal("accepted invalid draft kind")
	}
}

func TestHealthStrengthCommitOrder(t *testing.T) {
	d, ctx, _, v := strengthFixture(t)
	initial, err := d.HealthStrengthSetAdd(ctx, []StrengthSet{v})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err := strengthLock(ctx, tx); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 2)
	v.ID = strengthID(80)
	go func() { _, err := d.HealthStrengthSetAdd(ctx, []StrengthSet{v}); done <- err }()
	go func() { _, _, err := d.HealthStrengthSetUndo(ctx, initial[0].ID); done <- err }()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		var n int
		if err := d.pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event='advisory'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n >= 2 {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("writers did not wait")
		case <-tick.C:
		}
	}
	var seq int64
	if err := d.pool.QueryRow(ctx, `SELECT last_value FROM health_strength_set_seq`).Scan(&seq); err != nil || seq != initial[0].Seq {
		t.Fatal("allocated before lock", seq, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	changes, err := d.HealthStrengthSetChanges(ctx, seq, 10)
	if err != nil || len(changes) != 2 || changes[0].Seq >= changes[1].Seq {
		t.Fatal(changes, err)
	}
}

func TestHealthStrengthWorkoutLatestUsesHalfOpenDay(t *testing.T) {
	d, ctx, _, _ := strengthFixture(t)
	day := time.Date(2026, 11, 20, 0, 0, 0, 0, time.FixedZone("owner", 9*3600))
	if got, err := d.HealthStrengthWorkoutLatest(ctx, day, day.AddDate(0, 0, 1)); err != nil || got != nil {
		t.Fatal(got, err)
	}
	for i, offset := range []time.Duration{-time.Second, 0, 10 * time.Hour, 20 * time.Hour, 24 * time.Hour} {
		start := day.Add(offset)
		if _, err := d.HealthStrengthWorkoutAdd(ctx, StrengthWorkout{ID: strengthID(90 + i), Start: start, End: start.Add(time.Minute), Report: json.RawMessage(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := d.HealthStrengthWorkoutLatest(ctx, day, day.AddDate(0, 0, 1))
	if err != nil || got == nil || got.ID != strengthID(93) || !got.Start.Equal(day.Add(20*time.Hour)) {
		t.Fatal(got, err)
	}
}
