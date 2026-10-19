-- A public sign-up that has not confirmed its email yet. No users row
-- exists until the link in the email is opened. A later sign-up for the
-- same address replaces this one, and only its link works.
CREATE TABLE pending_signups (
    email         TEXT PRIMARY KEY,
    name          TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    locale        TEXT NOT NULL,
    token_hash    BYTEA NOT NULL,
    expires_at    TIMESTAMPTZ NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX pending_signups_token_hash_key ON pending_signups (token_hash);
