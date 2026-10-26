UPDATE telegram_links SET kinds = array_remove(kinds, 'comment_mention');
UPDATE email_notification_prefs SET kinds = array_remove(kinds, 'comment_mention');
UPDATE app_notification_prefs SET kinds = array_remove(kinds, 'comment_mention');
DELETE FROM notifications WHERE kind = 'comment_mention';
