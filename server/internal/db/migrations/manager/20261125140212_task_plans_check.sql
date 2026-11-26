-- task_plans.state holds only the plan states the code writes (#15397, docs/plans/plan-typed-core.md step P9):
-- planOpen ('open', also the column's default), planStarted and planDismissed in server/internal/assistant/tasks.go.
-- NOT VALID: new and updated rows are checked, existing rows are not scanned, so the deploy cannot fail on old data; a
-- later migration validates it once the hub's rows have been checked. TestStorageChecksMatchTheCode keeps this list and
-- the constants together.
ALTER TABLE task_plans ADD CONSTRAINT task_plans_state_check CHECK (state IN ('open', 'started', 'dismissed')) NOT VALID;
