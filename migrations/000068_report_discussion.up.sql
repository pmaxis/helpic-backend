-- The discussion on a sent report between its freelancer and client: about
-- one of its lines, or the report as a whole. A comment by the client on a
-- line opens a question on it, which stays open until either side resolves
-- it; an open question warns before a payment, but doesn't stop one.
CREATE TABLE report_comments (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    report_id  UUID NOT NULL REFERENCES reports (id) ON DELETE CASCADE,
    -- The line it's about; NULL for the report as a whole, or once the
    -- freelancer dropped the line from the report.
    item_id    UUID REFERENCES report_items (id) ON DELETE SET NULL,
    -- The line's task as it was named, so a comment on a dropped line still
    -- says what it was about; '' for the report as a whole.
    item_title TEXT NOT NULL DEFAULT '',
    author_id  UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    body       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX report_comments_report_idx ON report_comments (report_id, created_at);
CREATE INDEX report_comments_item_idx ON report_comments (item_id) WHERE item_id IS NOT NULL;

ALTER TABLE report_items ADD COLUMN question_open BOOLEAN NOT NULL DEFAULT false;

-- When the freelancer last took a sent report back to correct it: a draft
-- again, which the client doesn't see until it's sent again, and whose
-- discussion goes on.
ALTER TABLE reports ADD COLUMN recalled_at TIMESTAMPTZ;

-- Both new kinds are on wherever a user already chose kinds, as the
-- defaults have them for everyone else. A channel turned off stays off.
UPDATE telegram_links SET kinds = kinds || ARRAY['report_comment', 'report_recalled']
WHERE cardinality(kinds) > 0 AND NOT 'report_comment' = ANY (kinds);
UPDATE email_notification_prefs SET kinds = kinds || ARRAY['report_comment', 'report_recalled']
WHERE cardinality(kinds) > 0 AND NOT 'report_comment' = ANY (kinds);
UPDATE app_notification_prefs SET kinds = kinds || ARRAY['report_comment', 'report_recalled']
WHERE cardinality(kinds) > 0 AND NOT 'report_comment' = ANY (kinds);
