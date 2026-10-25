-- Browsers a user has signed in from. Each holds a random token in a
-- long-lived cookie of its own; only the token's SHA-256 is kept here. A
-- sign-in from a browser the account doesn't know is emailed to the user.
CREATE TABLE known_devices (
    user_id      UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash   BYTEA NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, token_hash)
);

CREATE INDEX known_devices_last_seen_idx ON known_devices (last_seen_at);
