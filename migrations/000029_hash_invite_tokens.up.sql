-- Invitation links, like reset and email-change links, keep only their
-- SHA-256: whoever reads the database (or the inviter's API) can't accept one.
-- Links already emailed keep working: the hash is of the same token.
ALTER TABLE invitations ADD COLUMN token_hash BYTEA;
UPDATE invitations SET token_hash = sha256(convert_to(token, 'UTF8'));
ALTER TABLE invitations ALTER COLUMN token_hash SET NOT NULL;
DROP INDEX invitations_token_key;
ALTER TABLE invitations DROP COLUMN token;
CREATE UNIQUE INDEX invitations_token_hash_key ON invitations (token_hash);

ALTER TABLE company_invites ADD COLUMN token_hash BYTEA;
UPDATE company_invites SET token_hash = sha256(convert_to(token, 'UTF8'));
ALTER TABLE company_invites ALTER COLUMN token_hash SET NOT NULL;
ALTER TABLE company_invites DROP COLUMN token;
CREATE UNIQUE INDEX company_invites_token_hash_key ON company_invites (token_hash);
