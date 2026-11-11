package db

import (
	"context"
	"testing"
	"time"
)

// A task whose agent never started is closed by the hub with its start error, and waits on the owner (#1077). Ticking it
// done is the owner closing it (#3713): no longer failed, done at that moment by them. Done again on a to-do that was
// simply done leaves it as it was; reopening a failed start still clears the error.
func TestTodoOwnerClosesAFailedStart(t *testing.T) {
	d := open(t)
	ctx := context.Background()
	base := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	for _, id := range []string{"todo_failed", "todo_done", "todo_retry"} {
		if err := d.CreateTodo(ctx, TodoRow{ID: id, Title: "Deploy the panel", CreatedAt: base, UpdatedAt: base}); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"todo_failed", "todo_retry"} {
		if _, err := d.FailTodo(ctx, id, "machine offline", base.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	done, notDone := true, false
	at := base.Add(time.Hour)
	row, err := d.EditTodo(ctx, "todo_failed", TodoEdit{Done: &done}, at)
	if err != nil || row.StartError != "" || row.DoneAt == nil || !row.DoneAt.Equal(at) || row.DoneBy != "" || row.DoneReason != "" {
		t.Fatalf("owner closing a failed start: %+v %v", row, err)
	}
	if got, err := d.GetTodo(ctx, "todo_failed"); err != nil || got.StartError != "" || !got.DoneAt.Equal(at) {
		t.Fatalf("stored: %+v %v", got, err)
	}

	first := base.Add(2 * time.Minute)
	if _, err := d.EditTodo(ctx, "todo_done", TodoEdit{Done: &done}, first); err != nil {
		t.Fatal(err)
	}
	row, err = d.EditTodo(ctx, "todo_done", TodoEdit{Done: &done}, at)
	if err != nil || row.DoneAt == nil || !row.DoneAt.Equal(first) {
		t.Fatalf("done twice moved done_at: %+v %v", row, err)
	}

	row, err = d.EditTodo(ctx, "todo_retry", TodoEdit{Done: &notDone}, at)
	if err != nil || row.StartError != "" || row.DoneAt != nil || row.ReopenedAt == nil {
		t.Fatalf("reopening a failed start: %+v %v", row, err)
	}
}
