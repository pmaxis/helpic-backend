-- The tokens themselves are gone: pending invitations get placeholder tokens,
-- so their links stop working and they have to be sent again.
ALTER TABLE invitations ADD COLUMN token TEXT;
UPDATE invitations SET token = encode(token_hash, 'hex');
ALTER TABLE invitations ALTER COLUMN token SET NOT NULL;
DROP INDEX invitations_token_hash_key;
ALTER TABLE invitations DROP COLUMN token_hash;
CREATE UNIQUE INDEX invitations_token_key ON invitations (token);

ALTER TABLE company_invites ADD COLUMN token TEXT;
UPDATE company_invites SET token = encode(token_hash, 'hex');
ALTER TABLE company_invites ALTER COLUMN token SET NOT NULL;
ALTER TABLE company_invites DROP COLUMN token_hash;
ALTER TABLE company_invites ADD CONSTRAINT company_invites_token_key UNIQUE (token);
