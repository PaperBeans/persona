CREATE TABLE assistant_external_bindings (
    id text PRIMARY KEY DEFAULT gen_random_uuid()::text,
    external_ref jsonb NOT NULL CHECK (jsonb_typeof(external_ref) = 'object' AND external_ref ? 'provider'),
    target_kind text NOT NULL CHECK (target_kind IN ('direction', 'todo')),
    target_id text NOT NULL CHECK (target_id <> ''),
    policy jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(policy) = 'object'),
    todo_target text GENERATED ALWAYS AS (CASE WHEN target_kind = 'todo' THEN target_id END) STORED,
    CONSTRAINT assistant_external_binding_unique UNIQUE (external_ref, target_kind, target_id),
    CONSTRAINT assistant_external_binding_todo FOREIGN KEY (todo_target) REFERENCES todos(id) ON DELETE CASCADE
);

CREATE INDEX assistant_external_bindings_target ON assistant_external_bindings (target_kind, target_id);
CREATE INDEX assistant_external_bindings_todo ON assistant_external_bindings (todo_target) WHERE todo_target IS NOT NULL;
CREATE INDEX assistant_external_bindings_provider ON assistant_external_bindings ((external_ref->>'provider'), (COALESCE(external_ref->>'channel', '')));

CREATE TABLE assistant_external_source_policies (
    external_ref jsonb PRIMARY KEY CHECK (jsonb_typeof(external_ref) = 'object' AND external_ref ? 'provider'),
    policy jsonb NOT NULL CHECK (jsonb_typeof(policy) = 'object')
);

-- Index the existing source of truth so links already saved, and every subsequent
-- repository edit, are visible to lookup in the same transaction as the original row.
CREATE INDEX todo_repository_sets_external_links ON todo_repository_sets USING gin (repositories jsonb_path_ops);
