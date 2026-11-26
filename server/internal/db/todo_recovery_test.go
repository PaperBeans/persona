package db

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestTodoRecoveryStateCRUD(t *testing.T) {
	d, ctx := open(t), context.Background()
	t0 := time.Date(2026, 11, 25, 16, 0, 0, 0, time.UTC)

	for _, id := range []string{"todo_rec_a", "todo_rec_b"} {
		if err := d.CreateTodo(ctx, TodoRow{
			ID:        id,
			Title:     "Supervised task " + id,
			CreatedAt: t0,
			UpdatedAt: t0,
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Missing row returns found=false, err=nil.
	if _, found, err := d.GetTodoRecoveryState(ctx, "todo_rec_a"); err != nil || found {
		t.Fatalf("expected missing recovery state, got found=%t err=%v", found, err)
	}

	// Record initial checkpoints.
	if err := d.RecordTodoActiveCheckpoint(ctx, TodoRecoveryState{
		TodoID:              "todo_rec_a",
		MachineID:           "mac-1",
		SessionID:           "sess-a1",
		LastActiveBootEpoch: "boot-1",
		UpdatedAt:           t0,
	}); err != nil {
		t.Fatal(err)
	}
	if err := d.RecordTodoActiveCheckpoint(ctx, TodoRecoveryState{
		TodoID:              "todo_rec_b",
		MachineID:           "mac-2",
		SessionID:           "sess-b1",
		LastActiveBootEpoch: "boot-1",
		UpdatedAt:           t0.Add(time.Second),
	}); err != nil {
		t.Fatal(err)
	}

	// List returns deterministic ordering by updated_at ASC, todo_id ASC.
	list, err := d.ListTodoRecoveryStates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].TodoID != "todo_rec_a" || list[1].TodoID != "todo_rec_b" {
		t.Fatalf("unexpected list order: %+v", list)
	}

	// Increment attempts atomically updates attempts, boot epoch, and last_recovered_at.
	t1 := t0.Add(2 * time.Minute)
	inc, err := d.IncrementTodoRecoveryAttempts(ctx, "todo_rec_a", "boot-2", t1)
	if err != nil {
		t.Fatal(err)
	}
	if inc.RecoveryAttempts != 1 || inc.LastActiveBootEpoch != "boot-2" || inc.LastRecoveredAt == nil || !inc.LastRecoveredAt.Equal(t1) {
		t.Fatalf("unexpected incremented state: %+v", inc)
	}

	// Subsequent checkpoint update preserves recovery_attempts and last_recovered_at.
	t2 := t0.Add(3 * time.Minute)
	if err := d.RecordTodoActiveCheckpoint(ctx, TodoRecoveryState{
		TodoID:              "todo_rec_a",
		MachineID:           "mac-1",
		SessionID:           "sess-a2",
		LastActiveBootEpoch: "boot-2",
		UpdatedAt:           t2,
	}); err != nil {
		t.Fatal(err)
	}
	got, found, err := d.GetTodoRecoveryState(ctx, "todo_rec_a")
	if err != nil || !found {
		t.Fatalf("GetTodoRecoveryState failed: found=%t err=%v", found, err)
	}
	if got.SessionID != "sess-a2" || got.RecoveryAttempts != 1 || got.LastRecoveredAt == nil || !got.LastRecoveredAt.Equal(t1) {
		t.Fatalf("checkpoint update did not preserve attempts: %+v", got)
	}

	// Incrementing a missing todo returns ErrTodoNotFound.
	if _, err := d.IncrementTodoRecoveryAttempts(ctx, "todo_missing", "boot-2", t2); !errors.Is(err, ErrTodoNotFound) {
		t.Fatalf("expected ErrTodoNotFound, got %v", err)
	}

	// Reset clears attempts and last_recovered_at.
	t3 := t0.Add(4 * time.Minute)
	if err := d.ResetTodoRecoveryState(ctx, "todo_rec_a", t3); err != nil {
		t.Fatal(err)
	}
	got, found, err = d.GetTodoRecoveryState(ctx, "todo_rec_a")
	if err != nil || !found {
		t.Fatalf("GetTodoRecoveryState after reset failed: found=%t err=%v", found, err)
	}
	if got.RecoveryAttempts != 0 || got.LastRecoveredAt != nil {
		t.Fatalf("expected reset attempts=0 and nil last_recovered_at, got %+v", got)
	}

	// Explicit delete removes the row.
	if err := d.DeleteTodoRecoveryState(ctx, "todo_rec_a"); err != nil {
		t.Fatal(err)
	}
	if _, found, err := d.GetTodoRecoveryState(ctx, "todo_rec_a"); err != nil || found {
		t.Fatalf("expected deleted row to be absent, found=%t err=%v", found, err)
	}

	// Deleting parent todo cascades to todo_recovery_state.
	if err := d.DeleteTodo(ctx, "todo_rec_b"); err != nil {
		t.Fatal(err)
	}
	if _, found, err := d.GetTodoRecoveryState(ctx, "todo_rec_b"); err != nil || found {
		t.Fatalf("expected cascade delete to remove recovery row, found=%t err=%v", found, err)
	}
}
