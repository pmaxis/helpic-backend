-- A mention is said to the user: it's on wherever they already chose kinds,
-- as the defaults have it for everyone else. A channel turned off (no kinds)
-- stays off.
UPDATE telegram_links SET kinds = kinds || ARRAY['comment_mention']
WHERE cardinality(kinds) > 0 AND NOT 'comment_mention' = ANY (kinds);
UPDATE email_notification_prefs SET kinds = kinds || ARRAY['comment_mention']
WHERE cardinality(kinds) > 0 AND NOT 'comment_mention' = ANY (kinds);
UPDATE app_notification_prefs SET kinds = kinds || ARRAY['comment_mention']
WHERE cardinality(kinds) > 0 AND NOT 'comment_mention' = ANY (kinds);
