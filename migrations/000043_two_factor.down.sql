-- Accounts go back to signing in with the password alone.
DROP TABLE two_factor_recovery_codes;
ALTER TABLE users DROP COLUMN two_factor_step, DROP COLUMN two_factor_pending, DROP COLUMN two_factor_secret;
