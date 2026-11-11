-- Per-criterion progress (#3635). done_when remains the ordered source of text;
-- rows written before this column are read as pending, without rewriting their history.
ALTER TABLE todo_supervision ADD COLUMN IF NOT EXISTS criteria jsonb NOT NULL DEFAULT '[]'::jsonb;
