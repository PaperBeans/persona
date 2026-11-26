-- Idle-archive finalization is durably retried after a manager restart. Its stable audit_id makes the
-- plugin_audit mirror idempotent without changing the append-only behavior of any other extension audit.
-- A previous recovery could mirror the same finalization twice. Keep the first durable row before enforcing
-- exactly one row per audit_id; no other extension audit action is touched.
DELETE FROM plugin_audit AS duplicate
USING plugin_audit AS original
WHERE duplicate.action IN ('session_idle_archive', 'session_idle_archive_refused')
  AND original.action = duplicate.action
  AND original.detail->>'audit_id' = duplicate.detail->>'audit_id'
  AND original.id < duplicate.id;

CREATE UNIQUE INDEX IF NOT EXISTS plugin_audit_idle_archive_audit_id
    ON plugin_audit ((detail->>'audit_id'))
    WHERE action IN ('session_idle_archive', 'session_idle_archive_refused')
      AND detail->>'audit_id' IS NOT NULL;
