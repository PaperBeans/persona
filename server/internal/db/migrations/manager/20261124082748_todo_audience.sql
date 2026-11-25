-- Additive to-do audience metadata (#14619): "" (legacy/unset), "human" (human-facing deliverable),
-- or "autonomous" (autonomous engineering task). Existing rows default to ''.
ALTER TABLE todos ADD COLUMN IF NOT EXISTS audience text NOT NULL DEFAULT ''
    CONSTRAINT todos_audience_valid CHECK (audience IN ('', 'human', 'autonomous'));
