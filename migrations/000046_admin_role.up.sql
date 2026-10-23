-- Admins are accounts of their own: they run the platform, not projects, so
-- they have no work and see none. An admin is made with the cli
-- (create-admin), never by signing up or accepting an invitation.
ALTER TABLE users DROP CONSTRAINT users_role_check;
ALTER TABLE users ADD CONSTRAINT users_role_check CHECK (role IN ('freelancer', 'client', 'admin'));
