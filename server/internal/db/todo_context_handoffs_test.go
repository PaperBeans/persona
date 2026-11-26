package db

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestTodoContextHandoffsCRUDIdempotencyAndCascade(t *testing.T) {
	d := open(t)
	ctx := context.Background()

	if _, err := d.pool.Exec(ctx, `INSERT INTO todos (id, title) VALUES ('todo_h1', 'Continue on codex')`); err != nil {
		t.Fatal(err)
	}

	t1 := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	first, created, err := d.CreateTodoContextHandoff(ctx, TodoContextHandoffRow{
		ID:             "handoff_1",
		OperationID:    "op_1",
		TodoID:         "todo_h1",
		SourceMachine:  "box",
		SourceSession:  "sess_old",
		SourceLauncher: "claude",
		SourceAgent:    "claude",
		TargetMachine:  "box",
		TargetLauncher: "codex",
		TargetAgent:    "codex",
		SourceRanges:   json.RawMessage(`[{"machine_id":"box","session_id":"sess_old","from_seq":1,"to_seq":5,"item_count":5}]`),
		BriefRevision:  "brief_1@2026-11-23T09:00:00Z",
		DiffRef:        json.RawMessage(`{"scope":"uncommitted","base_short":"abc1234"}`),
		Strategy:       "fresh_full_summary",
		Generator:      "deterministic_v1",
		Summary:        "saved summary v1",
		MissingSources: json.RawMessage(`[]`),
		DeliveryStatus: "pending",
		CreatedAt:      t1,
		UpdatedAt:      t1,
	})
	if err != nil {
		t.Fatalf("CreateTodoContextHandoff first: %v", err)
	}
	if !created || first == nil || first.ID != "handoff_1" || first.DeliveryStatus != "pending" {
		t.Fatalf("unexpected first create result: created=%v row=%+v", created, first)
	}

	// Duplicate (todo_id, operation_id) must stay idempotent and return the original row.
	dup, created, err := d.CreateTodoContextHandoff(ctx, TodoContextHandoffRow{
		ID:             "handoff_dup",
		OperationID:    "op_1",
		TodoID:         "todo_h1",
		Summary:        "should not overwrite",
		DeliveryStatus: "pending",
		CreatedAt:      t1.Add(time.Minute),
		UpdatedAt:      t1.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("CreateTodoContextHandoff duplicate: %v", err)
	}
	if created || dup == nil || dup.ID != "handoff_1" || dup.Summary != "saved summary v1" {
		t.Fatalf("duplicate op did not return original row: created=%v row=%+v", created, dup)
	}

	deliveredAt := t1.Add(2 * time.Minute)
	updated, err := d.UpdateTodoContextHandoffDelivery(ctx, "handoff_1", "box", "sess_new", "codex", "accepted", "", deliveredAt)
	if err != nil {
		t.Fatalf("UpdateTodoContextHandoffDelivery: %v", err)
	}
	if updated == nil || updated.DeliveryStatus != "accepted" || updated.TargetSession != "sess_new" || updated.DeliveredAt == nil {
		t.Fatalf("delivery update mismatch: %+v", updated)
	}

	// Superseding regeneration creates a second row while preserving the first.
	t2 := t1.Add(5 * time.Minute)
	second, created, err := d.CreateTodoContextHandoff(ctx, TodoContextHandoffRow{
		ID:             "handoff_2",
		OperationID:    "op_2",
		TodoID:         "todo_h1",
		SourceMachine:  "box",
		SourceSession:  "sess_new",
		SourceLauncher: "codex",
		TargetMachine:  "box",
		TargetLauncher: "profiles-ag",
		SupersedesID:   "handoff_1",
		Summary:        "saved summary v2",
		DeliveryStatus: "rejected",
		DeliveryError:  "launcher missing",
		CreatedAt:      t2,
		UpdatedAt:      t2,
	})
	if err != nil || !created || second == nil {
		t.Fatalf("CreateTodoContextHandoff second: created=%v row=%+v err=%v", created, second, err)
	}

	list, err := d.ListTodoContextHandoffs(ctx, "todo_h1")
	if err != nil {
		t.Fatalf("ListTodoContextHandoffs: %v", err)
	}
	if len(list) != 2 || list[0].ID != "handoff_2" || list[0].SupersedesID != "handoff_1" || list[1].ID != "handoff_1" {
		t.Fatalf("list ordering/preservation mismatch: %+v", list)
	}

	byOp, err := d.GetTodoContextHandoffByOperation(ctx, "todo_h1", "op_1")
	if err != nil || byOp == nil || byOp.ID != "handoff_1" {
		t.Fatalf("GetTodoContextHandoffByOperation mismatch: row=%+v err=%v", byOp, err)
	}

	// Cascade delete removes handoffs when the parent to-do is deleted.
	if _, err := d.pool.Exec(ctx, `DELETE FROM todos WHERE id = 'todo_h1'`); err != nil {
		t.Fatal(err)
	}
	afterDelete, err := d.ListTodoContextHandoffs(ctx, "todo_h1")
	if err != nil || len(afterDelete) != 0 {
		t.Fatalf("expected cascade delete to clear handoffs, got len=%d err=%v", len(afterDelete), err)
	}
}
