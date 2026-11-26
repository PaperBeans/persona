-- a2a_tasks.state holds only the states the A2A code writes (#15397, docs/plans/plan-typed-core.md step P9): the task
-- states of server/internal/assistant/a2a.go (a2aSubmitted, a2aWorking, a2aInput, a2aAuth, a2aComplete, a2aFailed,
-- a2aCanceled) and a2aQueued, which db.A2ACreate stores for a submitted task while its target is busy. The delivery
-- values (a2aSending, a2aDelivered, a2aUncertain, a2aExpired, a2aDiscarded) belong to events in body, never to state.
-- NOT VALID: new and updated rows are checked, existing rows are not scanned, so the deploy cannot fail on old data; a
-- later migration validates it once the hub's rows have been checked. TestStorageChecksMatchTheCode keeps this list and
-- the constants together.
ALTER TABLE a2a_tasks ADD CONSTRAINT a2a_tasks_state_check
    CHECK (state IN ('submitted', 'working', 'input_required', 'auth_required', 'completed', 'failed', 'canceled', 'queued'))
    NOT VALID;
