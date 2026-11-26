package db

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestTodoItemType(t *testing.T) {
	d := open(t)
	ctx := context.Background()
	base := time.Date(2026, 11, 25, 16, 30, 0, 0, time.UTC)

	// Valid creates: unset ("" -> SQL NULL), "personal", and "task".
	if err := d.CreateTodo(ctx, TodoRow{
		ID:        "todo_it_default",
		Title:     "Unset item type",
		Origin:    "conv_main",
		CreatedAt: base,
		UpdatedAt: base,
	}); err != nil {
		t.Fatalf("CreateTodo default item_type: %v", err)
	}
	if err := d.CreateTodo(ctx, TodoRow{
		ID:        "todo_it_personal",
		Title:     "Buy milk",
		ItemType:  ItemTypePersonal,
		CreatedAt: base.Add(time.Second),
		UpdatedAt: base.Add(time.Second),
	}); err != nil {
		t.Fatalf("CreateTodo personal item_type: %v", err)
	}
	if err := d.CreateTodo(ctx, TodoRow{
		ID:        "todo_it_task",
		Title:     "Ship release",
		Origin:    "conv_main",
		ItemType:  ItemTypeTask,
		CreatedAt: base.Add(2 * time.Second),
		UpdatedAt: base.Add(2 * time.Second),
	}); err != nil {
		t.Fatalf("CreateTodo task item_type: %v", err)
	}

	// Verify SQL NULL for unset row.
	var isNull bool
	if err := d.pool.QueryRow(ctx, `SELECT item_type IS NULL FROM todos WHERE id = $1`, "todo_it_default").Scan(&isNull); err != nil || !isNull {
		t.Fatalf("expected SQL NULL for unset item_type: isNull=%v err=%v", isNull, err)
	}

	// Invalid values rejected cleanly before write.
	for _, bad := range []string{"epic", "TASK", "personal ", "concern"} {
		err := d.CreateTodo(ctx, TodoRow{
			ID:        "todo_it_bad_" + bad,
			Title:     "Invalid",
			ItemType:  bad,
			CreatedAt: base,
			UpdatedAt: base,
		})
		if !errors.Is(err, ErrInvalidTodoItemType) {
			t.Fatalf("CreateTodo(%q): err=%v, want ErrInvalidTodoItemType", bad, err)
		}
	}

	// GetTodo & ListTodos preserve ItemType.
	for id, want := range map[string]string{
		"todo_it_default":  "",
		"todo_it_personal": ItemTypePersonal,
		"todo_it_task":     ItemTypeTask,
	} {
		got, err := d.GetTodo(ctx, id)
		if err != nil {
			t.Fatalf("GetTodo(%s): %v", id, err)
		}
		if got.ItemType != want {
			t.Fatalf("GetTodo(%s): item_type=%q, want %q", id, got.ItemType, want)
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
	if byID["todo_it_default"].ItemType != "" || byID["todo_it_personal"].ItemType != ItemTypePersonal || byID["todo_it_task"].ItemType != ItemTypeTask {
		t.Fatalf("ListTodos item_types unexpected: default=%q personal=%q task=%q",
			byID["todo_it_default"].ItemType, byID["todo_it_personal"].ItemType, byID["todo_it_task"].ItemType)
	}

	// EditTodo without ItemType leaves ItemType untouched.
	newTitle := "Buy oat milk"
	if row, err := d.EditTodo(ctx, "todo_it_personal", TodoEdit{Title: &newTitle}, base.Add(time.Minute)); err != nil || row.ItemType != ItemTypePersonal {
		t.Fatalf("EditTodo title only: row=%+v err=%v", row, err)
	}

	// EditTodo updates ItemType and clears back to SQL NULL with "".
	taskType := ItemTypeTask
	if row, err := d.EditTodo(ctx, "todo_it_personal", TodoEdit{ItemType: &taskType}, base.Add(2*time.Minute)); err != nil || row.ItemType != ItemTypeTask {
		t.Fatalf("EditTodo to task: row=%+v err=%v", row, err)
	}
	empty := ""
	if row, err := d.EditTodo(ctx, "todo_it_personal", TodoEdit{ItemType: &empty}, base.Add(3*time.Minute)); err != nil || row.ItemType != "" {
		t.Fatalf("EditTodo clear item_type: row=%+v err=%v", row, err)
	}
	if err := d.pool.QueryRow(ctx, `SELECT item_type IS NULL FROM todos WHERE id = $1`, "todo_it_personal").Scan(&isNull); err != nil || !isNull {
		t.Fatalf("expected SQL NULL after clearing item_type: isNull=%v err=%v", isNull, err)
	}

	// EditTodo rejects invalid ItemType cleanly.
	bad := "invalid"
	if _, err := d.EditTodo(ctx, "todo_it_task", TodoEdit{ItemType: &bad}, base.Add(4*time.Minute)); !errors.Is(err, ErrInvalidTodoItemType) {
		t.Fatalf("EditTodo invalid item_type: err=%v, want ErrInvalidTodoItemType", err)
	}
	got, err := d.GetTodo(ctx, "todo_it_task")
	if err != nil || got.ItemType != ItemTypeTask {
		t.Fatalf("failed EditTodo mutated item_type: got=%+v err=%v", got, err)
	}

	// Database CHECK constraint rejects both invalid strings and empty string directly.
	for _, rawVal := range []string{"bogus", ""} {
		if _, err := d.pool.Exec(ctx, `UPDATE todos SET item_type = $2 WHERE id = $1`, "todo_it_default", rawVal); err == nil {
			t.Fatalf("expected todos_item_type_valid CHECK constraint to reject %q", rawVal)
		}
	}
}

func TestTodoItemTypeMigrationUpgrade(t *testing.T) {
	ctx := context.Background()
	url := freshDatabase(t)

	// Migrate up to right before 20261125163718_todo_item_type.sql.
	before := buildOf(t, func(v int64) bool { return v < 20261125163718 }, nil)
	d, err := Attach(ctx, url, ManagerSet)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.migrateFrom(ctx, before); err != nil {
		t.Fatalf("migrate before item_type: %v", err)
	}

	// Insert a pre-existing legacy row before the column exists.
	base := time.Date(2026, 11, 25, 16, 0, 0, 0, time.UTC)
	if _, err := d.pool.Exec(ctx, `INSERT INTO todos (id, title, notes, created_at, updated_at, origin)
		VALUES ($1, $2, $3, $4, $4, $5)`, "todo_pre_item_type", "Legacy item", "keep me", base, "conv_legacy"); err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}

	// Apply remaining migrations including 20261125163718_todo_item_type.sql.
	if err := d.migrateFrom(ctx, withMigrations(t, nil)); err != nil {
		t.Fatalf("migrate with item_type: %v", err)
	}

	got, err := d.GetTodo(ctx, "todo_pre_item_type")
	if err != nil {
		t.Fatalf("GetTodo legacy row after upgrade: %v", err)
	}
	if got.Title != "Legacy item" || got.Notes != "keep me" || got.Origin != "conv_legacy" || got.ItemType != "" {
		t.Fatalf("legacy row after migration = %+v", got)
	}
	var isNull bool
	if err := d.pool.QueryRow(ctx, `SELECT item_type IS NULL FROM todos WHERE id = $1`, "todo_pre_item_type").Scan(&isNull); err != nil || !isNull {
		t.Fatalf("expected legacy row item_type IS NULL (zero backfill): isNull=%v err=%v", isNull, err)
	}
}
