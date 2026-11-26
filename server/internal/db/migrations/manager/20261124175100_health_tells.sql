-- Tell Health (#14714, docs/assistant/health.md "Tell Health"): keep text before its receipt.
-- Reserve a conversation before starting it. Only successful delivery clears the text.
CREATE TABLE IF NOT EXISTS health_tells (
    id text PRIMARY KEY,
    client_id text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    source text NOT NULL DEFAULT 'shortcut',
    text text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'received',
    attempts integer NOT NULL DEFAULT 0,
    error text NOT NULL DEFAULT '',
    new_session_id text NOT NULL DEFAULT '',
    machine_id text NOT NULL DEFAULT '',
    session_id text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS health_tells_pending ON health_tells (created_at, id) WHERE status = 'received';
