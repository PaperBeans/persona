-- Additive nullable to-do item_type column (#15758): explicit "personal" or "task" (NULL for unset/legacy rows; zero backfill).
-- Go constants: db.ItemTypePersonal ("personal"), db.ItemTypeTask ("task").
ALTER TABLE todos ADD COLUMN IF NOT EXISTS item_type text
    CONSTRAINT todos_item_type_valid CHECK (item_type IN ('personal', 'task'));
