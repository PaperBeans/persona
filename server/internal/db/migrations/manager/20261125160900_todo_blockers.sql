-- Persistent blocker graph for todos_v2 pull-based dependency reconciliation (#15731).
CREATE TABLE todo_blockers (
    id             text PRIMARY KEY,
    todo_id        text NOT NULL REFERENCES todos(id) ON DELETE CASCADE,
    kind           text NOT NULL CHECK (kind <> ''),
    target_ref     text NOT NULL CHECK (target_ref <> ''),
    condition_json text NOT NULL DEFAULT '{}',
    state          text NOT NULL DEFAULT 'active' CHECK (state IN ('active', 'resolved', 'cancelled')),
    attempts       integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    resolved_at    timestamptz
);

CREATE INDEX todo_blockers_todo_state_idx ON todo_blockers (todo_id, state);
CREATE INDEX todo_blockers_target_state_idx ON todo_blockers (target_ref, state);
