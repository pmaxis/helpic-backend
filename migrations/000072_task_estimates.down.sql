UPDATE telegram_links SET kinds = array_remove(array_remove(array_remove(kinds, 'estimate_sent'), 'estimate_approved'), 'estimate_changes_requested');
UPDATE email_notification_prefs SET kinds = array_remove(array_remove(array_remove(kinds, 'estimate_sent'), 'estimate_approved'), 'estimate_changes_requested');
UPDATE app_notification_prefs SET kinds = array_remove(array_remove(array_remove(kinds, 'estimate_sent'), 'estimate_approved'), 'estimate_changes_requested');
DELETE FROM notifications WHERE kind IN ('estimate_sent', 'estimate_approved', 'estimate_changes_requested');
DELETE FROM task_events WHERE kind IN ('estimate', 'estimate_sent', 'estimate_withdrawn', 'estimate_reviewed');
DROP TABLE task_estimates;
ALTER TABLE tasks DROP COLUMN estimate_seconds;
