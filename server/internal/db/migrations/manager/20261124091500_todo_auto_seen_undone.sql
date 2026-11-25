-- Persistent Undo protection for todos_v2 auto-seen (#14639): once the owner manually calls
-- Undo (DELETE /api/v1/todos/{id}/seen), background supervision / re-acceptance must never
-- silently re-apply auto-seen on this to-do.
ALTER TABLE todos ADD COLUMN IF NOT EXISTS auto_seen_undone boolean NOT NULL DEFAULT false;
