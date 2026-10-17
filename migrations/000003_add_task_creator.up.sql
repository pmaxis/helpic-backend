-- Tasks can now be created by either side; until now only clients could.
ALTER TABLE tasks ADD COLUMN created_by UUID REFERENCES users (id) ON DELETE CASCADE;
UPDATE tasks SET created_by = client_id;
ALTER TABLE tasks ALTER COLUMN created_by SET NOT NULL;
