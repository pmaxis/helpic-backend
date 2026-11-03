-- The time a task's freelancer expects it to take; NULL = not estimated.
-- Billed by the hour, it's what the client is asked to approve, at the rate.
ALTER TABLE tasks ADD COLUMN estimate_seconds BIGINT CHECK (estimate_seconds > 0);

-- An estimate the freelancer sent the client to approve: the task's billing,
-- time and price as they were when sent. The task changing after that makes
-- the estimate out of date (the app compares them); sending it again starts
-- the review over. review is NULL while the client hasn't answered.
CREATE TABLE task_estimates (
    task_id     UUID PRIMARY KEY REFERENCES tasks (id) ON DELETE CASCADE,
    billing     TEXT NOT NULL CHECK (billing IN ('fixed', 'hourly')),
    seconds     BIGINT CHECK (seconds > 0),
    -- the fixed price, or the time at the rate; NULL by the hour without one
    cents       BIGINT CHECK (cents >= 0),
    currency    TEXT NOT NULL,
    sent_by     UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    sent_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    review      TEXT CHECK (review IN ('approved', 'changes_requested')),
    -- what the client wrote asking for changes
    note        TEXT NOT NULL DEFAULT '',
    reviewed_by UUID REFERENCES users (id) ON DELETE SET NULL,
    reviewed_at TIMESTAMPTZ,
    CONSTRAINT task_estimates_reviewed_at CHECK ((review IS NULL) = (reviewed_at IS NULL)),
    CONSTRAINT task_estimates_amount CHECK (CASE billing WHEN 'fixed' THEN cents IS NOT NULL ELSE seconds IS NOT NULL END)
);

-- The new kinds are on wherever a user already chose kinds, as the defaults
-- have them for everyone else. A channel turned off stays off.
UPDATE telegram_links SET kinds = kinds || ARRAY['estimate_sent', 'estimate_approved', 'estimate_changes_requested']
WHERE cardinality(kinds) > 0 AND NOT 'estimate_sent' = ANY (kinds);
UPDATE email_notification_prefs SET kinds = kinds || ARRAY['estimate_sent', 'estimate_approved', 'estimate_changes_requested']
WHERE cardinality(kinds) > 0 AND NOT 'estimate_sent' = ANY (kinds);
UPDATE app_notification_prefs SET kinds = kinds || ARRAY['estimate_sent', 'estimate_approved', 'estimate_changes_requested']
WHERE cardinality(kinds) > 0 AND NOT 'estimate_sent' = ANY (kinds);
