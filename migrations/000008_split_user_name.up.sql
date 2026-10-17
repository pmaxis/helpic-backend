-- A user's name is now a first and a last name; `name` is kept as the full
-- name, computed from them, for everything that shows people.
ALTER TABLE users
    ADD COLUMN first_name TEXT NOT NULL DEFAULT '',
    ADD COLUMN last_name TEXT NOT NULL DEFAULT '';

UPDATE users SET
    first_name = split_part(trim(name), ' ', 1),
    last_name = trim(substr(trim(name), length(split_part(trim(name), ' ', 1)) + 1));

ALTER TABLE users DROP COLUMN name;
ALTER TABLE users ADD COLUMN name TEXT GENERATED ALWAYS AS (trim(first_name || ' ' || last_name)) STORED;
