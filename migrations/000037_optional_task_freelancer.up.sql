-- A task can be waiting for a freelancer: the client stays, the assignee can
-- be taken off or replaced.
ALTER TABLE tasks ALTER COLUMN freelancer_id DROP NOT NULL;
