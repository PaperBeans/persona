ALTER TABLE conversation_segments ADD COLUMN IF NOT EXISTS dispatched boolean NOT NULL DEFAULT false;
