-- Two-factor sign-in with an authenticator app. The secret is sealed with the
-- server's TWO_FACTOR_KEY: unlike a password it can't be hashed, the server
-- needs it to work the codes out. two_factor_pending is a secret shown during
-- setup and not yet confirmed with a code; two_factor_step is the last step a
-- code was used for, so no code works twice.
ALTER TABLE users
    ADD COLUMN two_factor_secret  BYTEA,
    ADD COLUMN two_factor_pending BYTEA,
    ADD COLUMN two_factor_step    BIGINT NOT NULL DEFAULT 0;

-- One-time codes for signing in without the phone. Only their hashes are
-- kept; a used one keeps its row, so how many are left can be told.
CREATE TABLE two_factor_recovery_codes (
    id        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id   UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    code_hash BYTEA NOT NULL,
    used_at   TIMESTAMPTZ
);

CREATE UNIQUE INDEX two_factor_recovery_codes_key ON two_factor_recovery_codes (user_id, code_hash);
