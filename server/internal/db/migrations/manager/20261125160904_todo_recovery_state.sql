-- Additive restart recovery state for supervised to-dos under todos_v2 (#15733).
CREATE TABLE todo_recovery_state (
    todo_id                text PRIMARY KEY REFERENCES todos(id) ON DELETE CASCADE,
    machine_id             text NOT NULL CHECK (machine_id <> ''),
    session_id             text NOT NULL CHECK (session_id <> ''),
    last_active_boot_epoch text NOT NULL CHECK (last_active_boot_epoch <> ''),
    recovery_attempts      integer NOT NULL DEFAULT 0 CHECK (recovery_attempts >= 0),
    last_recovered_at      timestamptz,
    updated_at             timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX todo_recovery_state_boot_epoch_idx ON todo_recovery_state (last_active_boot_epoch);
