-- Auditable context handoff artifacts for continuing a to-do on another provider or launcher (#12980).
CREATE TABLE todo_context_handoffs (
    id              text PRIMARY KEY,
    operation_id    text NOT NULL CHECK (operation_id <> ''),
    todo_id         text NOT NULL REFERENCES todos(id) ON DELETE CASCADE,
    source_machine  text NOT NULL DEFAULT '',
    source_session  text NOT NULL DEFAULT '',
    source_launcher text NOT NULL DEFAULT '',
    source_agent    text NOT NULL DEFAULT '',
    target_machine  text NOT NULL DEFAULT '',
    target_session  text NOT NULL DEFAULT '',
    target_launcher text NOT NULL DEFAULT '',
    target_agent    text NOT NULL DEFAULT '',
    source_ranges   jsonb NOT NULL DEFAULT '[]'::jsonb,
    brief_revision  text NOT NULL DEFAULT '',
    diff_ref        jsonb,
    strategy        text NOT NULL DEFAULT 'fresh_full_summary',
    generator       text NOT NULL DEFAULT 'deterministic_v1',
    summary         text NOT NULL DEFAULT '',
    missing_sources jsonb NOT NULL DEFAULT '[]'::jsonb,
    supersedes_id   text NOT NULL DEFAULT '',
    delivery_status text NOT NULL DEFAULT 'pending' CHECK (delivery_status IN ('pending', 'accepted', 'rejected', 'unknown')),
    delivery_error  text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    delivered_at    timestamptz
);

CREATE UNIQUE INDEX todo_context_handoffs_op_idx ON todo_context_handoffs (todo_id, operation_id);
CREATE INDEX todo_context_handoffs_todo_created_idx ON todo_context_handoffs (todo_id, created_at DESC, id DESC);
