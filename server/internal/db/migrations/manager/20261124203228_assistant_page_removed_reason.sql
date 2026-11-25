-- Dashboards (#15017): why an Agent removed a page (page remove's reason), shown to the owner in Recently removed.
-- Empty for the owner's own removals and the other ways a page goes.
ALTER TABLE assistant_pages ADD COLUMN IF NOT EXISTS removed_reason text NOT NULL DEFAULT ''
    CHECK (char_length(removed_reason) <= 200);
