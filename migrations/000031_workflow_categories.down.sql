ALTER TABLE workflow_statuses DROP COLUMN IF EXISTS category_id;

CREATE OR REPLACE FUNCTION ensure_workflow(f UUID, c UUID) RETURNS void AS $$
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

DROP TABLE IF EXISTS workflow_categories;
