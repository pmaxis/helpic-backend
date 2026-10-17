DROP TABLE email_notification_prefs;
DROP INDEX notifications_unemailed_idx;
ALTER TABLE notifications DROP COLUMN emailed_at;
