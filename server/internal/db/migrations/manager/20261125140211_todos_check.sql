-- todos.kind holds only what the code writes (#15397, docs/plans/plan-typed-core.md step P9): '' for a to-do or a task
-- (the column's default and db.TodoRow's zero value) or db.KindConcern ('concern', server/internal/db/todos.go); the
-- manager's todoKind turns "todo" into '' before a row is made.
-- NOT VALID: new and updated rows are checked, existing rows are not scanned, so the deploy cannot fail on old data; a
-- later migration validates it once the hub's rows have been checked. TestStorageChecksMatchTheCode keeps this list and
-- the constants together.
ALTER TABLE todos ADD CONSTRAINT todos_kind_check CHECK (kind IN ('', 'concern')) NOT VALID;
