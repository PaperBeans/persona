-- The hub's record of delivered alert pushes (hub/lnmode.go, #8257): one row per push, appended, swept by age and count.
-- It replaces the hub.push_sent setting, which every push rewrote whole; the hub moves that setting's entries here once
-- at start (AdoptPushSentSetting), so this migration only makes the table.
CREATE TABLE IF NOT EXISTS hub_push_sent (
    id          bigserial PRIMARY KEY,
    at          timestamptz NOT NULL,
    level       text NOT NULL DEFAULT '',
    related_key text NOT NULL DEFAULT '',
    manager     text NOT NULL DEFAULT ''
);
