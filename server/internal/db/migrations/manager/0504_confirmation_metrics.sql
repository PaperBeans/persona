ALTER TABLE interactions
    ADD COLUMN user_notified_at timestamptz,
    ADD COLUMN auto_authorized_at timestamptz,
    ADD COLUMN authorization_id text NOT NULL DEFAULT '',
    ADD COLUMN todo_id text NOT NULL DEFAULT '';

CREATE INDEX interactions_user_notified ON interactions (user_notified_at)
    WHERE user_notified_at IS NOT NULL;
CREATE INDEX interactions_auto_authorized ON interactions (auto_authorized_at)
    WHERE auto_authorized_at IS NOT NULL;
