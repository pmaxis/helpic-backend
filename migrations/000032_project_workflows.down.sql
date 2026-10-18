-- Point every task back at its pair's column, then drop the project workflows.
UPDATE tasks t
SET status_id = (
    SELECT ws.id FROM workflow_statuses ws
    WHERE ws.freelancer_id = t.freelancer_id
      AND ws.client_id IS NOT DISTINCT FROM t.client_id
      AND ws.project_id IS NULL
      AND ws.category = t.status
    ORDER BY ws.position
    LIMIT 1
)
WHERE status_id IN (SELECT id FROM workflow_statuses WHERE project_id IS NOT NULL);

DELETE FROM workflow_statuses WHERE project_id IS NOT NULL;
DELETE FROM workflow_categories WHERE project_id IS NOT NULL;

DROP FUNCTION IF EXISTS ensure_project_workflow(UUID);

CREATE OR REPLACE FUNCTION ensure_workflow(f UUID, c UUID) RETURNS void AS $$
BEGIN
    PERFORM pg_advisory_xact_lock(hashtext('workflow:' || f::text || ':' || coalesce(c::text, '')));
    IF NOT EXISTS (
        SELECT 1 FROM workflow_categories WHERE freelancer_id = f AND client_id IS NOT DISTINCT FROM c
    ) THEN
        INSERT INTO workflow_categories (
            freelancer_id, client_id, name, counts_open, counts_done, allows_delete, participants_can_move, position, system_key
        ) VALUES
            (f, c, 'New', true, false, true, true, 0, 'new'),
            (f, c, 'In progress', true, false, false, true, 1, 'in_progress'),
            (f, c, 'Done', false, true, false, false, 2, 'done'),
            (f, c, 'Cancelled', false, false, false, true, 3, 'cancelled');
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM workflow_statuses WHERE freelancer_id = f AND client_id IS NOT DISTINCT FROM c
    ) THEN
        INSERT INTO workflow_statuses (freelancer_id, client_id, name, category, category_id, position)
        SELECT f, c, cat.name,
            CASE
                WHEN cat.counts_done THEN 'done'
                WHEN NOT cat.counts_open THEN 'cancelled'
                WHEN cat.allows_delete THEN 'new'
                ELSE 'in_progress'
            END,
            cat.id, cat.position
        FROM workflow_categories cat
        WHERE cat.freelancer_id = f AND cat.client_id IS NOT DISTINCT FROM c;
    END IF;
END;
$$ LANGUAGE plpgsql;

DROP INDEX IF EXISTS workflow_statuses_project_idx;
DROP INDEX IF EXISTS workflow_categories_project_idx;
DROP INDEX IF EXISTS workflow_categories_name_key;

ALTER TABLE workflow_statuses DROP COLUMN IF EXISTS project_id;
ALTER TABLE workflow_categories DROP COLUMN IF EXISTS project_id;

CREATE UNIQUE INDEX workflow_categories_name_key
    ON workflow_categories (freelancer_id, COALESCE(client_id, '00000000-0000-0000-0000-000000000000'::uuid), lower(name));
