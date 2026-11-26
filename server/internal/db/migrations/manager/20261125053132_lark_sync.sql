-- The Lark user sync (docs/extensions/bots.md "Reading as you: daily sync"; internal/extmgr larksync*.go). The
-- accounts, bots, bindings and their sealed tokens are settings (bots.feishu.sync*); these are what a run reads and
-- hands over, committed together in one transaction per run, so a run that fails part way moves nothing.

-- Where each binding's readers stopped: one cursor per resource ("im:<chat id>", "drive", "wiki:<space id>"…).
CREATE TABLE IF NOT EXISTS lark_sync_cursors (
    binding_key text NOT NULL,
    resource text NOT NULL,
    cursor text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (binding_key, resource)
);

-- The dedup index across every binding: one line per piece of content (a message, a document, a minute), its
-- highest revision seen and every place it was read from.
CREATE TABLE IF NOT EXISTS lark_sync_index (
    canonical_key text PRIMARY KEY,
    kind text NOT NULL DEFAULT '',
    revision bigint NOT NULL DEFAULT 0,
    body_hash text NOT NULL DEFAULT '',
    sources jsonb NOT NULL DEFAULT '[]',
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- What the runs handed over, for the knowledge base's larksync connector to read: chat batches by sequence (its
-- archive cursor) and documents by key. Pruned after 30 days.
CREATE TABLE IF NOT EXISTS lark_sync_outbox (
    seq bigserial PRIMARY KEY,
    kind text NOT NULL CHECK (kind IN ('chat', 'doc')),
    canonical_key text NOT NULL DEFAULT '',
    payload jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS lark_sync_outbox_kind_seq ON lark_sync_outbox (kind, seq);
CREATE INDEX IF NOT EXISTS lark_sync_outbox_created ON lark_sync_outbox (created_at);
