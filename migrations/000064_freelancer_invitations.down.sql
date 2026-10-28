UPDATE telegram_links SET kinds = array_remove(kinds, 'freelancer_joined');
UPDATE email_notification_prefs SET kinds = array_remove(kinds, 'freelancer_joined');
UPDATE app_notification_prefs SET kinds = array_remove(kinds, 'freelancer_joined');
DELETE FROM notifications WHERE kind = 'freelancer_joined';
DROP TABLE freelancer_invitations;
