-- What the client made of a sent report: approved it, or asked for changes,
-- saying what (client_review_note, also in its discussion). The freelancer
-- answers changes asked for by taking the report back to correct it, which
-- starts the review over, or by keeping it as it is, saying why
-- (review_answer): the client then reviews it again.
ALTER TABLE reports
    ADD COLUMN client_review TEXT CHECK (client_review IN ('approved', 'changes_requested')),
    ADD COLUMN client_reviewed_at TIMESTAMPTZ,
    ADD COLUMN client_review_note TEXT NOT NULL DEFAULT '',
    ADD COLUMN review_answer TEXT NOT NULL DEFAULT '',
    ADD CONSTRAINT reports_client_review_at CHECK ((client_review IS NULL) = (client_reviewed_at IS NULL));

-- Both new kinds are on wherever a user already chose kinds, as the
-- defaults have them for everyone else. A channel turned off stays off.
UPDATE telegram_links SET kinds = kinds || ARRAY['report_approved', 'report_changes_requested', 'report_changes_declined']
WHERE cardinality(kinds) > 0 AND NOT 'report_approved' = ANY (kinds);
UPDATE email_notification_prefs SET kinds = kinds || ARRAY['report_approved', 'report_changes_requested', 'report_changes_declined']
WHERE cardinality(kinds) > 0 AND NOT 'report_approved' = ANY (kinds);
UPDATE app_notification_prefs SET kinds = kinds || ARRAY['report_approved', 'report_changes_requested', 'report_changes_declined']
WHERE cardinality(kinds) > 0 AND NOT 'report_approved' = ANY (kinds);
