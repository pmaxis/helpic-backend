-- When an email was given up on, so the admins' alerts can tell the new ones.
ALTER TABLE emails ADD COLUMN failed_at TIMESTAMPTZ;
UPDATE emails SET failed_at = next_attempt_at WHERE sent_at IS NULL AND attempts >= 8;
