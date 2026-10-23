-- Every sign-in to an admin account, and every attempt that got the
-- password or the code wrong, for the record: the panel only adds to it.
CREATE TABLE admin_sign_ins (
    id          BIGSERIAL PRIMARY KEY,
    at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    admin_id    UUID REFERENCES users (id) ON DELETE SET NULL,
    admin_email TEXT NOT NULL,
    outcome     TEXT NOT NULL CHECK (outcome IN ('ok', 'wrong_password', 'wrong_code', 'limited')),
    ip          TEXT NOT NULL DEFAULT '',
    user_agent  TEXT NOT NULL DEFAULT ''
);

CREATE INDEX admin_sign_ins_at_idx ON admin_sign_ins (id DESC);
