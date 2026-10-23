-- An admin can block an account: it can't sign in, and its sessions end.
ALTER TABLE users ADD COLUMN blocked_at TIMESTAMPTZ;

-- What admins did to accounts, for the record: the panel only adds to it.
-- The emails are kept as they were, so an entry still reads after either
-- account is gone.
CREATE TABLE admin_actions (
    id           BIGSERIAL PRIMARY KEY,
    at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    admin_id     UUID REFERENCES users (id) ON DELETE SET NULL,
    admin_email  TEXT NOT NULL,
    action       TEXT NOT NULL CHECK (action IN ('block', 'unblock', 'end_sessions', 'reset_two_factor')),
    target_id    UUID REFERENCES users (id) ON DELETE SET NULL,
    target_email TEXT NOT NULL,
    reason       TEXT NOT NULL DEFAULT ''
);

CREATE INDEX admin_actions_target_idx ON admin_actions (target_id, id DESC);
