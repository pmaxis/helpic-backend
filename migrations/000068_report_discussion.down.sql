UPDATE telegram_links SET kinds = array_remove(array_remove(kinds, 'report_comment'), 'report_recalled');
UPDATE email_notification_prefs SET kinds = array_remove(array_remove(kinds, 'report_comment'), 'report_recalled');
UPDATE app_notification_prefs SET kinds = array_remove(array_remove(kinds, 'report_comment'), 'report_recalled');
DELETE FROM notifications WHERE kind IN ('report_comment', 'report_recalled');
DELETE FROM report_events WHERE kind IN ('question', 'resolved', 'recalled');
ALTER TABLE reports DROP COLUMN recalled_at;
ALTER TABLE report_items DROP COLUMN question_open;
DROP TABLE report_comments;
