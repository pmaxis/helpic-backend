-- A signed-in device. The browser holds a short-lived access token (a JWT
-- naming the session) and a long-lived refresh token; only the refresh
-- token's SHA-256 is kept here. Each refresh swaps it for a new one: the one
-- it replaced stays in prev_refresh_hash, so a stolen token used after the
-- owner moved on is recognised and ends the session. Signing out deletes the
-- row.
CREATE TABLE sessions (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id           UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    refresh_hash      BYTEA NOT NULL,
    prev_refresh_hash BYTEA,
    rotated_at        TIMESTAMPTZ,
    user_agent        TEXT NOT NULL DEFAULT '',
    ip                TEXT NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at        TIMESTAMPTZ NOT NULL
);

CREATE UNIQUE INDEX sessions_refresh_hash_key ON sessions (refresh_hash);
CREATE INDEX sessions_prev_refresh_hash_idx ON sessions (prev_refresh_hash);
CREATE INDEX sessions_user_id_idx ON sessions (user_id);
