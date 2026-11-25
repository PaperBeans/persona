package db

import (
	"context"
	"testing"
	"time"
)

func TestTodoDirectionID(t *testing.T) {
	d := open(t)
	ctx := context.Background()
	base := time.Date(2026, 11, 24, 9, 30, 0, 0, time.UTC)

	// Create todos: default (""), explicit direction_id, padded direction_id (trimmed), whitespace-only (trimmed to "").
	if err := d.CreateTodo(ctx, TodoRow{
		ID:        "todo_dir_default",
		Title:     "Legacy default",
		Origin:    "conv_main",
		CreatedAt: base,
		UpdatedAt: base,
	}); err != nil {
		t.Fatalf("CreateTodo default direction_id: %v", err)
	}
	if err := d.CreateTodo(ctx, TodoRow{
		ID:          "todo_dir_explicit",
		Title:       "Explicit direction",
		Origin:      "conv_main",
		DirectionID: "dir_alpha",
		CreatedAt:   base.Add(time.Second),
		UpdatedAt:   base.Add(time.Second),
	}); err != nil {
		t.Fatalf("CreateTodo explicit direction_id: %v", err)
	}
	if err := d.CreateTodo(ctx, TodoRow{
		ID:          "todo_dir_trimmed",
		Title:       "Trimmed direction",
		Origin:      "conv_leader",
		DirectionID: "  dir_beta \t\n",
		CreatedAt:   base.Add(2 * time.Second),
		UpdatedAt:   base.Add(2 * time.Second),
	}); err != nil {
		t.Fatalf("CreateTodo trimmed direction_id: %v", err)
	}
	if err := d.CreateTodo(ctx, TodoRow{
		ID:          "todo_dir_blank",
		Title:       "Blank direction",
		Origin:      "conv_main",
		DirectionID: "   ",
		CreatedAt:   base.Add(3 * time.Second),
		UpdatedAt:   base.Add(3 * time.Second),
	}); err != nil {
		t.Fatalf("CreateTodo blank direction_id: %v", err)
	}

	// GetTodo & ListTodos preserve DirectionID and leave Origin untouched.
	wantByID := map[string]struct {
		dir    string
		origin string
	}{
		"todo_dir_default":  {"", "conv_main"},
		"todo_dir_explicit": {"dir_alpha", "conv_main"},
		"todo_dir_trimmed":  {"dir_beta", "conv_leader"},
		"todo_dir_blank":    {"", "conv_main"},
	}
	for id, want := range wantByID {
		got, err := d.GetTodo(ctx, id)
		if err != nil {
			t.Fatalf("GetTodo(%s): %v", id, err)
		}
		if got.DirectionID != want.dir || got.Origin != want.origin {
			t.Fatalf("GetTodo(%s): direction_id=%q origin=%q, want %q / %q", id, got.DirectionID, got.Origin, want.dir, want.origin)
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
	for id, want := range wantByID {
		if byID[id].DirectionID != want.dir || byID[id].Origin != want.origin {
			t.Fatalf("ListTodos(%s): direction_id=%q origin=%q, want %q / %q", id, byID[id].DirectionID, byID[id].Origin, want.dir, want.origin)
		}
	}

	// Attach an active agent to verify EditTodo on DirectionID never touches todo_agents or origin.
	if _, err := d.LinkTodoAgentWithSource(ctx, "todo_dir_explicit", "m1", "sess_running", "Worker", "/repo", "claude", "", "manual", "", base.Add(10*time.Second)); err != nil {
		t.Fatalf("LinkTodoAgentWithSource: %v", err)
	}

	// EditTodo without DirectionID leaves DirectionID untouched.
	newTitle := "Explicit direction v2"
	if row, err := d.EditTodo(ctx, "todo_dir_explicit", TodoEdit{Title: &newTitle}, base.Add(time.Minute)); err != nil || row.DirectionID != "dir_alpha" {
		t.Fatalf("EditTodo title only: row=%+v err=%v", row, err)
	}

	// EditTodo updates DirectionID (trims whitespace) and leaves Origin & linked agents untouched.
	nextDir := "  dir_gamma  "
	if row, err := d.EditTodo(ctx, "todo_dir_explicit", TodoEdit{DirectionID: &nextDir}, base.Add(2*time.Minute)); err != nil || row.DirectionID != "dir_gamma" || row.Origin != "conv_main" {
		t.Fatalf("EditTodo update direction_id: row=%+v err=%v", row, err)
	}

	// EditTodo clears DirectionID with "" (and whitespace-only string).
	empty := ""
	if row, err := d.EditTodo(ctx, "todo_dir_explicit", TodoEdit{DirectionID: &empty}, base.Add(3*time.Minute)); err != nil || row.DirectionID != "" || row.Origin != "conv_main" {
		t.Fatalf("EditTodo clear direction_id: row=%+v err=%v", row, err)
	}
	got, err := d.GetTodo(ctx, "todo_dir_explicit")
	if err != nil || got.DirectionID != "" || got.Origin != "conv_main" {
		t.Fatalf("GetTodo after clear: got=%+v err=%v", got, err)
	}

	spacesOnly := " \t "
	if row, err := d.EditTodo(ctx, "todo_dir_trimmed", TodoEdit{DirectionID: &spacesOnly}, base.Add(4*time.Minute)); err != nil || row.DirectionID != "" || row.Origin != "conv_leader" {
		t.Fatalf("EditTodo clear direction_id with whitespace: row=%+v err=%v", row, err)
	}

	agents, err := d.ListAllTodoAgents(ctx)
	if err != nil {
		t.Fatalf("ListAllTodoAgents: %v", err)
	}
	if len(agents) != 1 || agents[0].TodoID != "todo_dir_explicit" || agents[0].SessionID != "sess_running" || agents[0].ReleasedAt != nil {
		t.Fatalf("EditTodo direction_id mutated todo_agents: %+v", agents)
	}
}

func TestTodoDirectionIDMigrationUpgrade(t *testing.T) {
	ctx := context.Background()
	url := freshDatabase(t)

	// Migrate up to right before 20261124093000_todo_direction_id.sql.
	before := buildOf(t, func(v int64) bool { return v < 20261124093000 }, nil)
	d, err := Attach(ctx, url, ManagerSet)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.migrateFrom(ctx, before); err != nil {
		t.Fatalf("migrate before direction_id: %v", err)
	}

	// Insert a pre-existing legacy row before the column exists.
	base := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	if _, err := d.pool.Exec(ctx, `INSERT INTO todos (id, title, notes, created_at, updated_at, origin)
		VALUES ($1, $2, $3, $4, $4, $5)`, "todo_pre_dir_migration", "Legacy item", "keep me", base, "conv_legacy"); err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}

	// Apply the remaining migrations including 20261124093000_todo_direction_id.sql.
	if err := d.migrateFrom(ctx, withMigrations(t, nil)); err != nil {
		t.Fatalf("migrate with direction_id: %v", err)
	}

	got, err := d.GetTodo(ctx, "todo_pre_dir_migration")
	if err != nil {
		t.Fatalf("GetTodo legacy row after upgrade: %v", err)
	}
	if got.Title != "Legacy item" || got.Notes != "keep me" || got.Origin != "conv_legacy" || got.DirectionID != "" {
		t.Fatalf("legacy row after migration = %+v", got)
	}
}
