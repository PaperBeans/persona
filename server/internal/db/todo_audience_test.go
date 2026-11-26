package db

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestTodoAudience(t *testing.T) {
	d := open(t)
	ctx := context.Background()
	base := time.Date(2026, 11, 24, 8, 30, 0, 0, time.UTC)

	// Valid creates: unset (""), "human", and "autonomous".
	if err := d.CreateTodo(ctx, TodoRow{
		ID:        "todo_aud_default",
		Title:     "Legacy default",
		Origin:    "conv_main",
		CreatedAt: base,
		UpdatedAt: base,
	}); err != nil {
		t.Fatalf("CreateTodo default audience: %v", err)
	}
	if err := d.CreateTodo(ctx, TodoRow{
		ID:        "todo_aud_human",
		Title:     "Research report",
		Origin:    "conv_main",
		Audience:  AudienceHuman,
		CreatedAt: base.Add(time.Second),
		UpdatedAt: base.Add(time.Second),
	}); err != nil {
		t.Fatalf("CreateTodo human audience: %v", err)
	}
	if err := d.CreateTodo(ctx, TodoRow{
		ID:        "todo_aud_auto",
		Title:     "Fix flaky test",
		Origin:    "conv_main",
		Audience:  AudienceAutonomous,
		CreatedAt: base.Add(2 * time.Second),
		UpdatedAt: base.Add(2 * time.Second),
	}); err != nil {
		t.Fatalf("CreateTodo autonomous audience: %v", err)
	}

	// Invalid values rejected cleanly before write.
	for _, bad := range []string{"bot", "HUMAN", "autonomous ", "user"} {
		err := d.CreateTodo(ctx, TodoRow{
			ID:        "todo_aud_bad_" + bad,
			Title:     "Invalid",
			Audience:  bad,
			CreatedAt: base,
			UpdatedAt: base,
		})
		if !errors.Is(err, ErrInvalidTodoAudience) {
			t.Fatalf("CreateTodo(%q): err=%v, want ErrInvalidTodoAudience", bad, err)
		}
	}

	// GetTodo & ListTodos preserve Audience and leave Origin untouched.
	for id, want := range map[string]string{
		"todo_aud_default": "",
		"todo_aud_human":   AudienceHuman,
		"todo_aud_auto":    AudienceAutonomous,
	} {
		got, err := d.GetTodo(ctx, id)
		if err != nil {
			t.Fatalf("GetTodo(%s): %v", id, err)
		}
		if got.Audience != want || got.Origin != "conv_main" {
			t.Fatalf("GetTodo(%s): audience=%q origin=%q, want %q / conv_main", id, got.Audience, got.Origin, want)
		}
	}
	rows, err := d.ListTodos(ctx)
	if err != nil {
		t.Fatalf("ListTodos: %v", err)
	}
	byID := map[string]TodoRow{}
	for _, r := range rows {
		byID[r.ID] = r
	}
	if byID["todo_aud_default"].Audience != "" || byID["todo_aud_human"].Audience != AudienceHuman || byID["todo_aud_auto"].Audience != AudienceAutonomous {
		t.Fatalf("ListTodos audiences unexpected: default=%q human=%q auto=%q",
			byID["todo_aud_default"].Audience, byID["todo_aud_human"].Audience, byID["todo_aud_auto"].Audience)
	}

	// EditTodo without Audience leaves Audience untouched.
	newTitle := "Research report v2"
	if row, err := d.EditTodo(ctx, "todo_aud_human", TodoEdit{Title: &newTitle}, base.Add(time.Minute)); err != nil || row.Audience != AudienceHuman {
		t.Fatalf("EditTodo title only: row=%+v err=%v", row, err)
	}

	// EditTodo updates Audience and clears with "".
	auto := AudienceAutonomous
	if row, err := d.EditTodo(ctx, "todo_aud_human", TodoEdit{Audience: &auto}, base.Add(2*time.Minute)); err != nil || row.Audience != AudienceAutonomous {
		t.Fatalf("EditTodo to autonomous: row=%+v err=%v", row, err)
	}
	empty := ""
	if row, err := d.EditTodo(ctx, "todo_aud_human", TodoEdit{Audience: &empty}, base.Add(3*time.Minute)); err != nil || row.Audience != "" {
		t.Fatalf("EditTodo clear audience: row=%+v err=%v", row, err)
	}
	got, err := d.GetTodo(ctx, "todo_aud_human")
	if err != nil || got.Audience != "" {
		t.Fatalf("GetTodo after clear: got=%+v err=%v", got, err)
	}

	// EditTodo rejects invalid Audience cleanly.
	bad := "invalid"
	if _, err := d.EditTodo(ctx, "todo_aud_auto", TodoEdit{Audience: &bad}, base.Add(4*time.Minute)); !errors.Is(err, ErrInvalidTodoAudience) {
		t.Fatalf("EditTodo invalid audience: err=%v, want ErrInvalidTodoAudience", err)
	}
	got, err = d.GetTodo(ctx, "todo_aud_auto")
	if err != nil || got.Audience != AudienceAutonomous {
		t.Fatalf("failed EditTodo mutated audience: got=%+v err=%v", got, err)
	}

	// Database CHECK constraint also rejects invalid values directly.
	if _, err := d.pool.Exec(ctx, `UPDATE todos SET audience = 'bogus' WHERE id = $1`, "todo_aud_default"); err == nil {
		t.Fatalf("expected todos_audience_valid CHECK constraint to reject 'bogus'")
	}
}

func TestTodoAudienceMigrationUpgrade(t *testing.T) {
	ctx := context.Background()
	url := freshDatabase(t)

	// Migrate up to right before 20261124082748_todo_audience.sql.
	before := buildOf(t, func(v int64) bool { return v < 20261124082748 }, nil)
	d, err := Attach(ctx, url, ManagerSet)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.migrateFrom(ctx, before); err != nil {
		t.Fatalf("migrate before audience: %v", err)
	}

	// Insert a pre-existing legacy row before the column exists.
	base := time.Date(2026, 11, 24, 8, 0, 0, 0, time.UTC)
	if _, err := d.pool.Exec(ctx, `INSERT INTO todos (id, title, notes, created_at, updated_at, origin)
		VALUES ($1, $2, $3, $4, $4, $5)`, "todo_pre_migration", "Legacy item", "keep me", base, "conv_legacy"); err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}

	// Apply the remaining migrations including 20261124082748_todo_audience.sql.
	if err := d.migrateFrom(ctx, withMigrations(t, nil)); err != nil {
		t.Fatalf("migrate with audience: %v", err)
	}

	got, err := d.GetTodo(ctx, "todo_pre_migration")
	if err != nil {
		t.Fatalf("GetTodo legacy row after upgrade: %v", err)
	}
	if got.Title != "Legacy item" || got.Notes != "keep me" || got.Origin != "conv_legacy" || got.Audience != "" {
		t.Fatalf("legacy row after migration = %+v", got)
	}
}
