-- What Agents and assistant conversations moved between the user's computers with file_transfer (#14683,
-- docs/protocol/file-transfer.md "Agents and the assistant"): one row per call that named two computers, done,
-- refused or failed. Paths and sizes only: never a file's content, never a credential.
--   caller_machine / caller_session / caller_kind: who called the tool (an Agent's session, or an assistant
--   conversation; kind agent or assistant). direction: send (from the caller's computer) or fetch (to it).
--   from_* / to_*: the computer read and the one written; to_path is the destination asked for ("" the receive
--   folder), result_path where it landed. kind: file or dir; bytes what was streamed (a folder's zip), files and
--   skipped a folder's files and what was left out (credentials, links). state: done or failed, with code and error.
CREATE TABLE IF NOT EXISTS file_transfer_audit (
    id             text PRIMARY KEY,
    at             timestamptz NOT NULL DEFAULT now(),
    caller_machine text NOT NULL DEFAULT '',
    caller_session text NOT NULL DEFAULT '',
    caller_kind    text NOT NULL DEFAULT '',
    direction      text NOT NULL DEFAULT '',
    from_machine   text NOT NULL,
    from_path      text NOT NULL DEFAULT '',
    to_machine     text NOT NULL,
    to_path        text NOT NULL DEFAULT '',
    result_path    text NOT NULL DEFAULT '',
    kind           text NOT NULL DEFAULT '',
    bytes          bigint NOT NULL DEFAULT 0,
    files          integer NOT NULL DEFAULT 0,
    skipped        integer NOT NULL DEFAULT 0,
    sha256         text NOT NULL DEFAULT '',
    state          text NOT NULL,
    code           text NOT NULL DEFAULT '',
    error          text NOT NULL DEFAULT '',
    elapsed_ms     bigint NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS file_transfer_audit_at ON file_transfer_audit (at);
