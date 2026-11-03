-- A freelancer or a client can stop working with the other. The other side
-- hears of it wherever they already chose kinds, as the defaults have it for
-- everyone else. A channel turned off (no kinds) stays off.
UPDATE telegram_links SET kinds = kinds || ARRAY['client_left', 'freelancer_left']
WHERE cardinality(kinds) > 0 AND NOT 'client_left' = ANY (kinds);
UPDATE email_notification_prefs SET kinds = kinds || ARRAY['client_left', 'freelancer_left']
WHERE cardinality(kinds) > 0 AND NOT 'client_left' = ANY (kinds);
UPDATE app_notification_prefs SET kinds = kinds || ARRAY['client_left', 'freelancer_left']
WHERE cardinality(kinds) > 0 AND NOT 'client_left' = ANY (kinds);
