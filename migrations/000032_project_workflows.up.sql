-- A project's categories and columns are its own. The pair's workflow
-- (project_id NULL) stays for tasks that aren't in a project.
ALTER TABLE workflow_categories
    ADD COLUMN project_id UUID REFERENCES projects (id) ON DELETE CASCADE;

ALTER TABLE workflow_statuses
    ADD COLUMN project_id UUID REFERENCES projects (id) ON DELETE CASCADE;

DROP INDEX workflow_categories_name_key;

CREATE UNIQUE INDEX workflow_categories_name_key
    ON workflow_categories (
        freelancer_id,
        COALESCE(client_id, '00000000-0000-0000-0000-000000000000'::uuid),
        COALESCE(project_id, '00000000-0000-0000-0000-000000000000'::uuid),
        lower(name)
    );

CREATE INDEX workflow_categories_project_idx ON workflow_categories (project_id) WHERE project_id IS NOT NULL;
CREATE INDEX workflow_statuses_project_idx ON workflow_statuses (project_id) WHERE project_id IS NOT NULL;

-- Copies a pair's categories and columns onto each of its projects, then
-- points that project's tasks at the copies. Projects with no pair workflow
-- yet get the defaults below.
CREATE TEMP TABLE category_map (
    old_id     UUID NOT NULL,
    new_id     UUID NOT NULL,
    project_id UUID NOT NULL
) ON COMMIT DROP;

INSERT INTO category_map (old_id, new_id, project_id)
SELECT c.id, gen_random_uuid(), p.id
FROM projects p
JOIN workflow_categories c
  ON c.freelancer_id = p.freelancer_id
 AND c.client_id IS NOT DISTINCT FROM p.client_id
 AND c.project_id IS NULL;

INSERT INTO workflow_categories (
    id, freelancer_id, client_id, project_id, name, counts_open, counts_done,
    allows_delete, participants_can_move, position, system_key
)
SELECT m.new_id, c.freelancer_id, c.client_id, m.project_id, c.name, c.counts_open, c.counts_done,
    c.allows_delete, c.participants_can_move, c.position, c.system_key
FROM category_map m
JOIN workflow_categories c ON c.id = m.old_id;

CREATE TEMP TABLE column_map (
    old_id     UUID NOT NULL,
    new_id     UUID NOT NULL,
    project_id UUID NOT NULL
) ON COMMIT DROP;

INSERT INTO column_map (old_id, new_id, project_id)
SELECT ws.id, gen_random_uuid(), m.project_id
FROM workflow_statuses ws
JOIN category_map m ON m.old_id = ws.category_id
WHERE ws.project_id IS NULL;

INSERT INTO workflow_statuses (id, freelancer_id, client_id, project_id, name, category, category_id, position, created_at)
SELECT cm.new_id, ws.freelancer_id, ws.client_id, cm.project_id, ws.name, ws.category, m.new_id, ws.position, ws.created_at
FROM column_map cm
JOIN workflow_statuses ws ON ws.id = cm.old_id
JOIN category_map m ON m.old_id = ws.category_id AND m.project_id = cm.project_id;

UPDATE tasks t
SET status_id = cm.new_id
FROM column_map cm
WHERE t.project_id = cm.project_id
  AND t.status_id = cm.old_id;

-- The pair workflow is only the rows with no project. A project's columns must
-- not count as the pair already having one.
CREATE OR REPLACE FUNCTION ensure_workflow(f UUID, c UUID) RETURNS void AS $$
BEGIN
    PERFORM pg_advisory_xact_lock(hashtext('workflow:' || f::text || ':' || coalesce(c::text, '')));
    IF NOT EXISTS (
        SELECT 1 FROM workflow_categories
        WHERE freelancer_id = f AND client_id IS NOT DISTINCT FROM c AND project_id IS NULL
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
        SELECT 1 FROM workflow_statuses
        WHERE freelancer_id = f AND client_id IS NOT DISTINCT FROM c AND project_id IS NULL
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
        WHERE cat.freelancer_id = f AND cat.client_id IS NOT DISTINCT FROM c AND cat.project_id IS NULL;
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
    IF NOT EXISTS (SELECT 1 FROM workflow_categories WHERE project_id = p) THEN
        INSERT INTO workflow_categories (
            freelancer_id, client_id, project_id, name, counts_open, counts_done, allows_delete, participants_can_move, position, system_key
        ) VALUES
            (f, c, p, 'New', true, false, true, true, 0, 'new'),
            (f, c, p, 'In progress', true, false, false, true, 1, 'in_progress'),
            (f, c, p, 'Done', false, true, false, false, 2, 'done'),
            (f, c, p, 'Cancelled', false, false, false, true, 3, 'cancelled');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM workflow_statuses WHERE project_id = p) THEN
        INSERT INTO workflow_statuses (freelancer_id, client_id, project_id, name, category, category_id, position)
        SELECT f, c, p, cat.name,
            CASE
                WHEN cat.counts_done THEN 'done'
                WHEN NOT cat.counts_open THEN 'cancelled'
                WHEN cat.allows_delete THEN 'new'
                ELSE 'in_progress'
            END,
            cat.id, cat.position
        FROM workflow_categories cat
        WHERE cat.project_id = p;
    END IF;
END;
$$ LANGUAGE plpgsql;

SELECT ensure_project_workflow(id)
FROM projects pr
WHERE NOT EXISTS (SELECT 1 FROM workflow_categories c WHERE c.project_id = pr.id);
