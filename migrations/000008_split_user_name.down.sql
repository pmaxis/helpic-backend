ALTER TABLE users ADD COLUMN full_name TEXT NOT NULL DEFAULT '';
UPDATE users SET full_name = name;
ALTER TABLE users DROP COLUMN name;
ALTER TABLE users RENAME COLUMN full_name TO name;
ALTER TABLE users DROP COLUMN first_name, DROP COLUMN last_name;
