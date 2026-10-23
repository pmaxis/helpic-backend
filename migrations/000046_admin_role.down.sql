-- Admin accounts have no work to keep; they go with their role.
DELETE FROM users WHERE role = 'admin';
ALTER TABLE users DROP CONSTRAINT users_role_check;
ALTER TABLE users ADD CONSTRAINT users_role_check CHECK (role IN ('freelancer', 'client'));
