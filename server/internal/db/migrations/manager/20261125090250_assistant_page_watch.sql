-- Dashboards phase 2 (#13020): watch pages, their latest 30 readings, and data-only grants for bound routines.
ALTER TABLE assistant_pages
    ADD COLUMN IF NOT EXISTS watch jsonb CHECK (watch IS NULL OR jsonb_typeof(watch) = 'object'),
    ADD COLUMN IF NOT EXISTS on_overview boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS routine_id text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS refresh_asked_at timestamptz,
    ADD COLUMN IF NOT EXISTS refresh_device text NOT NULL DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS assistant_pages_routine ON assistant_pages (routine_id) WHERE routine_id <> '';

-- The latest 30 readings of a watch page, trimmed in the transaction that writes one.
CREATE TABLE IF NOT EXISTS assistant_page_readings (
    id         bigserial PRIMARY KEY,
    page_id    text NOT NULL REFERENCES assistant_pages (id) ON DELETE CASCADE,
    reading    jsonb NOT NULL CHECK (jsonb_typeof(reading) = 'object'),
    alert_text text NOT NULL DEFAULT '' CHECK (char_length(alert_text) <= 120),
    note       text NOT NULL DEFAULT '' CHECK (char_length(note) <= 120),
    by_kind    text NOT NULL CHECK (by_kind IN ('agent', 'routine')),
    by_machine text NOT NULL DEFAULT '',
    by_session text NOT NULL DEFAULT '',
    at         timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS assistant_page_readings_page ON assistant_page_readings (page_id, at DESC, id DESC);

-- Data-only grants on a dashboard page (a bound routine, or later a to-do agent), deleted with the page.
CREATE TABLE IF NOT EXISTS assistant_page_grants (
    page_id      text NOT NULL REFERENCES assistant_pages (id) ON DELETE CASCADE,
    grantee_kind text NOT NULL CHECK (grantee_kind IN ('routine', 'todo_agent')),
    grantee_ref  text NOT NULL CHECK (grantee_ref <> ''),
    rights       text NOT NULL DEFAULT 'data' CHECK (rights = 'data'),
    granted_by   text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL,
    PRIMARY KEY (page_id, grantee_kind, grantee_ref)
);

CREATE INDEX IF NOT EXISTS assistant_page_grants_grantee ON assistant_page_grants (grantee_kind, grantee_ref);
