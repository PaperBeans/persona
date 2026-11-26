package db

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestTodoAutoSeenUndoGuard(t *testing.T) {
	d := open(t)
	ctx := context.Background()
	base := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)

	if err := d.CreateTodo(ctx, TodoRow{
		ID:        "todo_auto_seen_1",
		Title:     "Fix flaky build",
		Origin:    "conv_main",
		Audience:  AudienceAutonomous,
		CreatedAt: base,
		UpdatedAt: base,
	}); err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	got, err := d.GetTodo(ctx, "todo_auto_seen_1")
	if err != nil {
		t.Fatalf("GetTodo: %v", err)
	}
	if got.SeenAt != nil || got.AutoSeenUndone {
		t.Fatalf("initial row: seen_at=%v auto_seen_undone=%t", got.SeenAt, got.AutoSeenUndone)
	}

	// AutoSeeTodo applies when auto_seen_undone is false and leaves updated_at untouched.
	autoAt := base.Add(time.Minute)
	row, applied, err := d.AutoSeeTodo(ctx, "todo_auto_seen_1", autoAt)
	if err != nil || !applied || row == nil || row.SeenAt == nil || !row.SeenAt.Equal(autoAt) || row.AutoSeenUndone {
		t.Fatalf("AutoSeeTodo initial: row=%+v applied=%t err=%v", row, applied, err)
	}
	if !row.UpdatedAt.Equal(base) {
		t.Fatalf("AutoSeeTodo changed updated_at: got %v, want %v", row.UpdatedAt, base)
	}

	// Manual Undo (SeeTodo with nil) clears seen_at and persists auto_seen_undone = true.
	undone, err := d.SeeTodo(ctx, "todo_auto_seen_1", nil)
	if err != nil || undone == nil || undone.SeenAt != nil || !undone.AutoSeenUndone {
		t.Fatalf("SeeTodo(nil) undo: row=%+v err=%v", undone, err)
	}
	if !undone.UpdatedAt.Equal(base) {
		t.Fatalf("SeeTodo(nil) changed updated_at: got %v, want %v", undone.UpdatedAt, base)
	}

	// Subsequent AutoSeeTodo must never overwrite manual Undo.
	row, applied, err = d.AutoSeeTodo(ctx, "todo_auto_seen_1", base.Add(2*time.Minute))
	if err != nil || applied || row != nil {
		t.Fatalf("AutoSeeTodo after undo: row=%+v applied=%t err=%v, want (nil, false, nil)", row, applied, err)
	}
	got, err = d.GetTodo(ctx, "todo_auto_seen_1")
	if err != nil || got.SeenAt != nil || !got.AutoSeenUndone {
		t.Fatalf("GetTodo after blocked AutoSeeTodo: got=%+v err=%v", got, err)
	}

	// Manual PUT /seen (SeeTodo with non-nil time) sets seen_at without clearing auto_seen_undone.
	manualAt := base.Add(3 * time.Minute)
	manual, err := d.SeeTodo(ctx, "todo_auto_seen_1", &manualAt)
	if err != nil || manual == nil || manual.SeenAt == nil || !manual.SeenAt.Equal(manualAt) || !manual.AutoSeenUndone {
		t.Fatalf("SeeTodo(manual) after undo: row=%+v err=%v", manual, err)
	}

	// Missing to-do returns ErrTodoNotFound.
	if _, _, err := d.AutoSeeTodo(ctx, "todo_missing", autoAt); !errors.Is(err, ErrTodoNotFound) {
		t.Fatalf("AutoSeeTodo missing: err=%v, want ErrTodoNotFound", err)
	}
}

func TestTodoAutoSeenMigrationUpgrade(t *testing.T) {
	ctx := context.Background()
	url := freshDatabase(t)

	// Migrate up to right before 20261124091500_todo_auto_seen_undone.sql.
	before := buildOf(t, func(v int64) bool { return v < 20261124091500 }, nil)
	d, err := Attach(ctx, url, ManagerSet)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.migrateFrom(ctx, before); err != nil {
		t.Fatalf("migrate before auto_seen_undone: %v", err)
	}

	base := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	if _, err := d.pool.Exec(ctx, `INSERT INTO todos (id, title, notes, created_at, updated_at, origin, audience)
		VALUES ($1, $2, $3, $4, $4, $5, $6)`, "todo_pre_auto_seen", "Existing task", "keep", base, "conv_legacy", AudienceAutonomous); err != nil {
		t.Fatalf("insert pre-migration row: %v", err)
	}

	if err := d.migrateFrom(ctx, withMigrations(t, nil)); err != nil {
		t.Fatalf("migrate with auto_seen_undone: %v", err)
	}

	got, err := d.GetTodo(ctx, "todo_pre_auto_seen")
	if err != nil {
		t.Fatalf("GetTodo after upgrade: %v", err)
	}
	if got.Title != "Existing task" || got.Audience != AudienceAutonomous || got.AutoSeenUndone {
		t.Fatalf("upgraded row = %+v", got)
	}
}
