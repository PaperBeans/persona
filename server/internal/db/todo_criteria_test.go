package db

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestTodoCriteriaMigrationAndRoundTrip(t *testing.T) {
	ctx := context.Background()
	url := migratedDatabase(t, ManagerSet)
	d, err := Open(ctx, url, ManagerSet)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	// Recreate a pre-criteria row, then exercise the real additive migration.
	if _, err := d.pool.Exec(ctx, `ALTER TABLE todo_supervision DROP COLUMN criteria;
		DELETE FROM schema_migrations WHERE part = 'manager' AND version = 488;
		INSERT INTO todos (id, title) VALUES ('legacy', 'example');
		INSERT INTO todo_supervision (todo_id, supervisor, done_when, reviews)
		VALUES ('legacy', 'main', '["build","test"]', '[{"verdict":"rework","note":"keep history"}]')`); err != nil {
		t.Fatal(err)
	}
	if err := d.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := d.ListTodoSupervision(ctx)
	if err != nil || len(rows) != 1 {
		t.Fatalf("list: %+v %v", rows, err)
	}
	r := rows[0]
	if string(r.Reviews) != `[{"note": "keep history", "verdict": "rework"}]` ||
		!reflect.DeepEqual(r.DoneWhen, []string{"build", "test"}) ||
		!reflect.DeepEqual(r.Criteria, []TodoCriterion{{Criterion: "build", State: "pending"}, {Criterion: "test", State: "pending"}}) {
		t.Fatalf("legacy data lost: %+v, reviews %s", r, r.Reviews)
	}
	at := time.Now().UTC().Truncate(time.Microsecond)
	r.Criteria[1] = TodoCriterion{Criterion: "test", State: "met", Evidence: "go test: PASS", At: &at, By: "agent:box/s1"}
	r.UpdatedAt = at
	if err := d.PutTodoSupervision(ctx, r); err != nil {
		t.Fatal(err)
	}
	rows, err = d.ListTodoSupervision(ctx)
	if err != nil || len(rows) != 1 || !reflect.DeepEqual(rows[0].Criteria, r.Criteria) {
		t.Fatalf("round trip: %+v %v", rows, err)
	}
	r.DoneWhen = []string{"test", "deploy"}
	if err := d.PutTodoSupervision(ctx, r); err != nil {
		t.Fatal(err)
	}
	rows, err = d.ListTodoSupervision(ctx)
	if err != nil || len(rows) != 1 || !reflect.DeepEqual(rows[0].Criteria,
		[]TodoCriterion{r.Criteria[1], {Criterion: "deploy", State: "pending"}}) {
		t.Fatalf("reordered/new criteria: %+v %v", rows, err)
	}
}
