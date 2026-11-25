-- Persist watch status on post-closure PR follow-up subscriptions (#12371). When GitHub is not connected or the PR's
-- host has no code host that can read review/conflict state, the row records that it is not being watched instead of
-- pretending to poll, surfaces honestly in briefings, and returns to active watching once a later sweep polls the PR.
ALTER TABLE todo_pr_followups
    ADD COLUMN IF NOT EXISTS watch_status text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS watch_reason text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS watch_updated_at timestamptz;
