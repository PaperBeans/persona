-- Additive to-do direction membership metadata (#14660): decouples organizational direction
-- grouping from runtime supervision owner (supervision.supervisor). Existing rows default to ''.
ALTER TABLE todos ADD COLUMN IF NOT EXISTS direction_id text NOT NULL DEFAULT '';
