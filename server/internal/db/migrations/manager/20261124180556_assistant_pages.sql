-- Dashboards (#15017): pages an assistant conversation drafts and the owner keeps in the sidebar, their recent
-- versions, and one row per tap on a page, which is also the tap's delivery outbox. Only the default manager has pages.
CREATE TABLE IF NOT EXISTS assistant_pages (
    id              text PRIMARY KEY CHECK (id ~ '^pg_[0-9a-f]{16}$'),
    status          text NOT NULL CHECK (status IN ('draft', 'active', 'removed')),
    title           text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 40),
    -- the title as kept pages are compared (NFKC, case folded): no two kept pages share one
    title_key       text NOT NULL,
    icon            text NOT NULL DEFAULT 'chart',
    icon_color      text NOT NULL DEFAULT '',
    -- space, direction and todo are for later phases; the check lists them now so it never has to change
    owner_kind      text NOT NULL CHECK (owner_kind IN ('main', 'conversation', 'space', 'direction', 'todo')),
    owner_machine   text NOT NULL DEFAULT '',
    -- empty for main: the main conversation is looked up when a tap is delivered
    owner_ref       text NOT NULL DEFAULT '',
    created_machine text NOT NULL,
    created_session text NOT NULL,
    -- the preview card (assistant_views.id) the owner keeps the draft from
    offer_view      text NOT NULL DEFAULT '',
    why             text NOT NULL DEFAULT '',
    view            jsonb NOT NULL CHECK (jsonb_typeof(view) = 'object'),
    glance          jsonb,
    alert           jsonb,
    version         bigint NOT NULL DEFAULT 1 CHECK (version BETWEEN 1 AND 9007199254740991),
    pinned          boolean NOT NULL DEFAULT false,
    pinned_at       timestamptz,
    paused          boolean NOT NULL DEFAULT false,
    paused_by       text NOT NULL DEFAULT '' CHECK (paused_by IN ('', 'owner', 'restore')),
    kept_device     text NOT NULL DEFAULT '',
    kept_at         timestamptz,
    -- this page's write window; the window of all pages together is the setting assistant.pages_rate
    writes_window_start timestamptz NOT NULL,
    writes_window_n     integer NOT NULL DEFAULT 0 CHECK (writes_window_n >= 0),
    created_at      timestamptz NOT NULL,
    updated_at      timestamptz NOT NULL,
    content_at      timestamptz NOT NULL,
    removed_at      timestamptz,
    removed_by      text NOT NULL DEFAULT ''
        CHECK (removed_by IN ('', 'owner', 'agent', 'conversation_deleted', 'declined', 'expired')),
    CHECK ((status = 'removed') = (removed_at IS NOT NULL AND removed_by <> '')),
    CHECK (NOT pinned OR status = 'active' AND pinned_at IS NOT NULL)
);

CREATE INDEX IF NOT EXISTS assistant_pages_list ON assistant_pages (status, pinned, content_at DESC);
CREATE INDEX IF NOT EXISTS assistant_pages_owner ON assistant_pages (owner_kind, owner_machine, owner_ref)
    WHERE status <> 'removed';
CREATE INDEX IF NOT EXISTS assistant_pages_drafted ON assistant_pages (created_machine, created_session)
    WHERE status = 'draft';
CREATE UNIQUE INDEX IF NOT EXISTS assistant_pages_offer ON assistant_pages (offer_view) WHERE offer_view <> '';
CREATE UNIQUE INDEX IF NOT EXISTS assistant_pages_kept_title ON assistant_pages (title_key) WHERE status = 'active';

-- The latest 20 versions of each page, trimmed in the transaction that writes one.
CREATE TABLE IF NOT EXISTS assistant_page_versions (
    page_id    text NOT NULL REFERENCES assistant_pages (id) ON DELETE CASCADE,
    version    bigint NOT NULL CHECK (version BETWEEN 1 AND 9007199254740991),
    title      text NOT NULL,
    view       jsonb NOT NULL CHECK (jsonb_typeof(view) = 'object'),
    by_kind    text NOT NULL CHECK (by_kind IN ('agent', 'routine', 'todo_agent', 'owner', 'restore')),
    by_machine text NOT NULL DEFAULT '',
    by_session text NOT NULL DEFAULT '',
    by_device  text NOT NULL DEFAULT '',
    note       text NOT NULL DEFAULT '',
    at         timestamptz NOT NULL,
    PRIMARY KEY (page_id, version)
);

-- One row per tap (a page's buttons can be tapped every day), kept 90 days and at most 200 a page. delivered_at,
-- turn_started_at and turn_ended_at time the owner's wait: tap to delivery (queue time), and the turn that answered.
CREATE TABLE IF NOT EXISTS assistant_page_actions (
    id                  text PRIMARY KEY CHECK (id ~ '^pa_[0-9a-f]{16}$'),
    page_id             text NOT NULL REFERENCES assistant_pages (id) ON DELETE CASCADE,
    dedupe              text NOT NULL,
    action              text NOT NULL,
    sub_page            text NOT NULL DEFAULT '',
    values              jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(values) = 'object'),
    page_version        bigint NOT NULL CHECK (page_version BETWEEN 1 AND 9007199254740991),
    text                text NOT NULL,
    message_id          text NOT NULL UNIQUE,
    target_machine      text NOT NULL DEFAULT '',
    target_conversation text NOT NULL DEFAULT '',
    redirected          boolean NOT NULL DEFAULT false,
    status              text NOT NULL CHECK (status IN ('pending', 'sent', 'failed')),
    revision            integer NOT NULL DEFAULT 0 CHECK (revision >= 0),
    claimed_until       timestamptz,
    error               text NOT NULL DEFAULT '',
    device              text NOT NULL DEFAULT '',
    at                  timestamptz NOT NULL,
    updated_at          timestamptz NOT NULL,
    delivered_at        timestamptz,
    turn_started_at     timestamptz,
    turn_ended_at       timestamptz
);

CREATE INDEX IF NOT EXISTS assistant_page_actions_pending ON assistant_page_actions (updated_at) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS assistant_page_actions_page ON assistant_page_actions (page_id, at DESC);
CREATE INDEX IF NOT EXISTS assistant_page_actions_turn ON assistant_page_actions (target_machine, target_conversation)
    WHERE status = 'sent' AND turn_ended_at IS NULL;
