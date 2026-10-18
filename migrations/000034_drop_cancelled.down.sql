-- Brings the cancelled bucket back. Tasks that moved to Done stay there.
ALTER TABLE tasks DROP CONSTRAINT tasks_status_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_status_check
    CHECK (status IN ('new', 'in_progress', 'done', 'cancelled'));

ALTER TABLE workflow_statuses DROP CONSTRAINT workflow_statuses_category_check;
ALTER TABLE workflow_statuses ADD CONSTRAINT workflow_statuses_category_check
    CHECK (category IN ('new', 'in_progress', 'done', 'cancelled'));

INSERT INTO workflow_statuses (freelancer_id, client_id, project_id, name, category, position)
SELECT freelancer_id, client_id, project_id, 'Cancelled', 'cancelled', coalesce(max(position) + 1, 0)
FROM workflow_statuses
GROUP BY freelancer_id, client_id, project_id;

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
            (f, c, 'Done', 'done', 2),
            (f, c, 'Cancelled', 'cancelled', 3);
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
            (f, c, p, 'Done', 'done', 2),
            (f, c, p, 'Cancelled', 'cancelled', 3);
    END IF;
END;
$$ LANGUAGE plpgsql;
