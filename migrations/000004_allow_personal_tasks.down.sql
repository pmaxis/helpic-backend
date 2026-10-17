DELETE FROM tasks WHERE client_id IS NULL;
ALTER TABLE tasks DROP CONSTRAINT IF EXISTS tasks_personal_by_freelancer;
ALTER TABLE tasks ALTER COLUMN client_id SET NOT NULL;
