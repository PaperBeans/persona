-- Feed body updates retain the original conversation position. A separate indexed
-- timestamp lets reconnecting clients recover dismissal and Undo, including Undo
-- after dismissed_at has been removed from the body.
ALTER TABLE assistant_feed ADD COLUMN updated_at timestamptz;
CREATE INDEX assistant_feed_updated ON assistant_feed (conversation, updated_at)
    WHERE kind = 'message' AND updated_at IS NOT NULL;
CREATE INDEX assistant_feed_dismissed_ref ON assistant_feed (ref)
    WHERE kind = 'message' AND body ? 'dismissal_id';
