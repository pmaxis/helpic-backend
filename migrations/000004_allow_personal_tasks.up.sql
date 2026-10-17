-- A freelancer's personal task has no client. Only the freelancer may create
-- one, so a task without a client is always created by its freelancer.
ALTER TABLE tasks ALTER COLUMN client_id DROP NOT NULL;
ALTER TABLE tasks ADD CONSTRAINT tasks_personal_by_freelancer
    CHECK (client_id IS NOT NULL OR created_by = freelancer_id);
