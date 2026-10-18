-- Cancelled is no longer a status. Its tasks move to the workflow's Done
-- column. A board that has no Done column keeps this one and counts it as done.
UPDATE tasks t
SET status_id = d.id,
    status = 'done',
    position = NULL,
    completed_at = coalesce(t.completed_at, now())
FROM workflow_statuses src
JOIN LATERAL (
    SELECT id
    FROM workflow_statuses done
    WHERE done.freelancer_id = src.freelancer_id
      AND done.client_id IS NOT DISTINCT FROM src.client_id
      AND done.project_id IS NOT DISTINCT FROM src.project_id
      AND done.category = 'done'
    ORDER BY done.position
    LIMIT 1
) d ON true
WHERE t.status_id = src.id
  AND src.category = 'cancelled';

UPDATE workflow_statuses src
SET category = 'done',
    name = CASE WHEN src.name = 'Cancelled' THEN 'Done' ELSE src.name END
WHERE src.category = 'cancelled'
  AND NOT EXISTS (
      SELECT 1 FROM workflow_statuses done
      WHERE done.id <> src.id
        AND done.freelancer_id = src.freelancer_id
        AND done.client_id IS NOT DISTINCT FROM src.client_id
        AND done.project_id IS NOT DISTINCT FROM src.project_id
        AND done.category = 'done'
  );

UPDATE tasks
SET status = 'done',
    completed_at = coalesce(completed_at, now())
WHERE status = 'cancelled';

DELETE FROM workflow_statuses WHERE category = 'cancelled';

ALTER TABLE tasks DROP CONSTRAINT tasks_status_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_status_check
    CHECK (status IN ('new', 'in_progress', 'done'));

ALTER TABLE workflow_statuses DROP CONSTRAINT workflow_statuses_category_check;
ALTER TABLE workflow_statuses ADD CONSTRAINT workflow_statuses_category_check
    CHECK (category IN ('new', 'in_progress', 'done'));

CREATE OR REPLACE FUNCTION ensure_workflow(f UUID, c UUID) RETURNS void AS $$
BEGIN
    PERFORM pg_advisory_xact_lock(hashtext('workflow:' || f::text || ':' || coalesce(c::text, '')));
    IF NOT EXISTS (
        SELECT 1 FROM workflow_statuses
        WHERE freelancer_id = f AND client_id IS NOT DISTINCT FROM c AND project_id IS NULL
    ) THEN
        INSERT INTO workflow_statuses (freelancer_id, client_id, name, category, position) VALUES
            (f, c, 'New', 'new', 0),
            (f, c, 'In progress', 'in_progress', 1),
            (f, c, 'Done', 'done', 2);
    END IF;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION ensure_project_workflow(p UUID) RETURNS void AS $$
DECLARE
    f UUID;
    c UUID;
BEGIN
    SELECT freelancer_id, client_id INTO f, c FROM projects WHERE id = p;
    IF NOT FOUND THEN
        RETURN;
    END IF;
    PERFORM pg_advisory_xact_lock(hashtext('workflow:' || p::text));
    IF NOT EXISTS (SELECT 1 FROM workflow_statuses WHERE project_id = p) THEN
        INSERT INTO workflow_statuses (freelancer_id, client_id, project_id, name, category, position) VALUES
            (f, c, p, 'New', 'new', 0),
            (f, c, p, 'In progress', 'in_progress', 1),
            (f, c, p, 'Done', 'done', 2);
    END IF;
END;
$$ LANGUAGE plpgsql;
