-- Client OPAQUE registration, separate from the administrator's password. No plaintext password or reusable hash.
CREATE TABLE IF NOT EXISTS client_password (
    id integer PRIMARY KEY CHECK (id = 1),
    enabled boolean NOT NULL,
    revision text NOT NULL,
    server_keys bytea NOT NULL,
    credential_id bytea NOT NULL,
    record bytea NOT NULL
);
