-- Expiring device credentials (such as temporary read-only verification sessions) carry a nullable expiry timestamp.
ALTER TABLE device_tokens
    ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ;
