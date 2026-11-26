CREATE TABLE assistant_external_inbox_events (
    id text PRIMARY KEY,
    external_ref jsonb NOT NULL,
    event_id text NOT NULL,
    input jsonb NOT NULL,
    received_at timestamptz NOT NULL,
    ready_at timestamptz NOT NULL,
    plan jsonb,
    completed boolean NOT NULL DEFAULT false,
    UNIQUE (external_ref, event_id)
);

CREATE INDEX assistant_external_inbox_ready
    ON assistant_external_inbox_events (ready_at, id) WHERE NOT completed;
CREATE INDEX assistant_external_inbox_owner
    ON assistant_external_inbox_events (external_ref, received_at DESC)
    WHERE plan ? 'owner';
