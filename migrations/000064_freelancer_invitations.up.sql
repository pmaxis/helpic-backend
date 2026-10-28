-- A client's invitation for a freelancer: invitations the other way round.
-- The freelancer opens the emailed link and makes a freelancer account, or
-- signs in to theirs; from then on the two are linked. Only a client acting
-- for themselves invites: a company's owner, never its members. Links keep
-- only the SHA-256 of their token.
CREATE TABLE freelancer_invitations (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    client_id   UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    email       TEXT NOT NULL,
    name        TEXT NOT NULL DEFAULT '',
    token_hash  BYTEA NOT NULL,
    expires_at  TIMESTAMPTZ NOT NULL,
    accepted_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX freelancer_invitations_token_hash_key ON freelancer_invitations (token_hash);
-- One open invitation per email per client.
CREATE UNIQUE INDEX freelancer_invitations_pending_key ON freelancer_invitations (client_id, email) WHERE accepted_at IS NULL;

-- The client hears their freelancer joined wherever they already chose
-- kinds, as the defaults have it for everyone else. A channel turned off
-- (no kinds) stays off.
UPDATE telegram_links SET kinds = kinds || ARRAY['freelancer_joined']
WHERE cardinality(kinds) > 0 AND NOT 'freelancer_joined' = ANY (kinds);
UPDATE email_notification_prefs SET kinds = kinds || ARRAY['freelancer_joined']
WHERE cardinality(kinds) > 0 AND NOT 'freelancer_joined' = ANY (kinds);
UPDATE app_notification_prefs SET kinds = kinds || ARRAY['freelancer_joined']
WHERE cardinality(kinds) > 0 AND NOT 'freelancer_joined' = ANY (kinds);
