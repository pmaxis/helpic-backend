UPDATE telegram_links SET kinds = array_remove(array_remove(array_remove(kinds, 'report_approved'), 'report_changes_requested'), 'report_changes_declined');
UPDATE email_notification_prefs SET kinds = array_remove(array_remove(array_remove(kinds, 'report_approved'), 'report_changes_requested'), 'report_changes_declined');
UPDATE app_notification_prefs SET kinds = array_remove(array_remove(array_remove(kinds, 'report_approved'), 'report_changes_requested'), 'report_changes_declined');
DELETE FROM notifications WHERE kind IN ('report_approved', 'report_changes_requested', 'report_changes_declined');
DELETE FROM report_events WHERE kind IN ('approved', 'changes_requested', 'changes_declined');
ALTER TABLE reports DROP CONSTRAINT reports_client_review_at, DROP COLUMN review_answer, DROP COLUMN client_review_note,
    DROP COLUMN client_reviewed_at, DROP COLUMN client_review;
