-- A supervised task the done judge found stopped on a decision only the owner can make (#1277,
-- docs/assistant/unified-entry.md "Supervision"): when that stretch of work began, when it was judged and why, as JSON
-- the assistant owns. While it holds, the task cannot be accepted as done. NULL when it does not hold.
ALTER TABLE todo_supervision ADD COLUMN IF NOT EXISTS waits jsonb;
