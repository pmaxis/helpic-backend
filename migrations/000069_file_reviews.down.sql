UPDATE telegram_links SET kinds = array_remove(array_remove(kinds, 'file_approved'), 'file_changes_requested');
UPDATE email_notification_prefs SET kinds = array_remove(array_remove(kinds, 'file_approved'), 'file_changes_requested');
UPDATE app_notification_prefs SET kinds = array_remove(array_remove(kinds, 'file_approved'), 'file_changes_requested');
DELETE FROM notifications WHERE kind IN ('file_approved', 'file_changes_requested');
DELETE FROM task_events WHERE kind = 'file_reviewed';
ALTER TABLE comments DROP CONSTRAINT comments_pin_check, DROP COLUMN attachment_id, DROP COLUMN pin_x, DROP COLUMN pin_y;
ALTER TABLE attachments DROP COLUMN review, DROP COLUMN reviewed_by, DROP COLUMN reviewed_at;
DROP INDEX attachments_series_version_idx;
ALTER TABLE attachments DROP COLUMN series_id, DROP COLUMN version;
