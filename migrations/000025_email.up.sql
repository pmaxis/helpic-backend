-- Outgoing email, sent by a background job so a request never waits on the
-- mail server and a failed send is tried again later. Once sent, the bodies
-- are cleared: they can hold one-time links.
CREATE TABLE emails (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    to_address      TEXT NOT NULL,
    subject         TEXT NOT NULL,
    html            TEXT NOT NULL,
    text            TEXT NOT NULL,
    attempts        INT NOT NULL DEFAULT 0,
    last_error      TEXT NOT NULL DEFAULT '',
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at         TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX emails_pending_idx ON emails (next_attempt_at) WHERE sent_at IS NULL;

-- A one-time link that sets a new password. Only its SHA-256 is kept.
CREATE TABLE password_resets (
    token_hash BYTEA PRIMARY KEY,
    user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX password_resets_user_idx ON password_resets (user_id);

-- A sign-in email waiting to be confirmed from its own inbox. Only the
-- link's SHA-256 is kept.
CREATE TABLE email_changes (
    token_hash BYTEA PRIMARY KEY,
    user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    new_email  TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX email_changes_user_idx ON email_changes (user_id);
