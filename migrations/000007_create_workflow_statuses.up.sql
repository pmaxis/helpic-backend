-- Jira-style workflows: every freelancer–client pair (and every freelancer's
-- personal work, client_id NULL) has its own list of statuses, which are the
-- columns of their shared board. Each status belongs to a category — the
-- fixed new / in_progress / done / cancelled — which keeps the role rules,
-- open counts and list filters working whatever the statuses are called.
-- This replaces the per-user board columns.
DROP TABLE IF EXISTS task_placements;
DROP TABLE IF EXISTS board_columns;

CREATE TABLE workflow_statuses (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    freelancer_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    client_id     UUID REFERENCES users (id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    category      TEXT NOT NULL CHECK (category IN ('new', 'in_progress', 'done', 'cancelled')),
    position      INT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX workflow_statuses_pair_idx ON workflow_statuses (freelancer_id, client_id, position);

-- Creates a pair's default statuses the first time they are needed.
CREATE FUNCTION ensure_workflow(f UUID, c UUID) RETURNS void AS $$
BEGIN
    PERFORM pg_advisory_xact_lock(hashtext('workflow:' || f::text || ':' || coalesce(c::text, '')));
    IF NOT EXISTS (
        SELECT 1 FROM workflow_statuses WHERE freelancer_id = f AND client_id IS NOT DISTINCT FROM c
    ) THEN
        INSERT INTO workflow_statuses (freelancer_id, client_id, name, category, position) VALUES
            (f, c, 'New', 'new', 0),
            (f, c, 'In progress', 'in_progress', 1),
            (f, c, 'Done', 'done', 2),
            (f, c, 'Cancelled', 'cancelled', 3);
    END IF;
END;
$$ LANGUAGE plpgsql;

-- tasks.status stays the category; status_id is the exact status (column),
-- and position the task's place in it, shared by both sides.
ALTER TABLE tasks
    ADD COLUMN status_id UUID REFERENCES workflow_statuses (id),
    ADD COLUMN position INT;

SELECT ensure_workflow(freelancer_id, client_id)
FROM (SELECT DISTINCT freelancer_id, client_id FROM tasks) pairs;

UPDATE tasks t SET status_id = (
    SELECT ws.id FROM workflow_statuses ws
    WHERE ws.freelancer_id = t.freelancer_id
      AND ws.client_id IS NOT DISTINCT FROM t.client_id
      AND ws.category = t.status
    ORDER BY ws.position
    LIMIT 1
);

ALTER TABLE tasks ALTER COLUMN status_id SET NOT NULL;
CREATE INDEX tasks_status_id_idx ON tasks (status_id, position);
