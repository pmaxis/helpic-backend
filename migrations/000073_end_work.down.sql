UPDATE telegram_links SET kinds = array_remove(array_remove(kinds, 'client_left'), 'freelancer_left');
UPDATE email_notification_prefs SET kinds = array_remove(array_remove(kinds, 'client_left'), 'freelancer_left');
UPDATE app_notification_prefs SET kinds = array_remove(array_remove(kinds, 'client_left'), 'freelancer_left');
DELETE FROM notifications WHERE kind IN ('client_left', 'freelancer_left');
