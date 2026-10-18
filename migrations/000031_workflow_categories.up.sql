-- Categories belong to a board and are named by whoever manages it. A column
-- points at one. tasks.status and workflow_statuses.category stay the bucket
-- the rest of the product already understands: new, in progress, done or
-- cancelled, derived from the category's settings.
CREATE TABLE workflow_categories (
    id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    freelancer_id          UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    client_id              UUID REFERENCES users (id) ON DELETE CASCADE,
    name                   TEXT NOT NULL,
    counts_open            BOOLEAN NOT NULL DEFAULT true,
    counts_done            BOOLEAN NOT NULL DEFAULT false,
    allows_delete          BOOLEAN NOT NULL DEFAULT false,
    participants_can_move  BOOLEAN NOT NULL DEFAULT true,
    position               INT NOT NULL,
    -- Set for the four Helpic starts with, so a renamed "New" still resolves
    -- when an older client sends category "new". Empty for one the owner added.
    system_key             TEXT,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX workflow_categories_name_key
    ON workflow_categories (freelancer_id, COALESCE(client_id, '00000000-0000-0000-0000-000000000000'::uuid), lower(name));

CREATE INDEX workflow_categories_pair_idx ON workflow_categories (freelancer_id, client_id, position);

INSERT INTO workflow_categories (
    freelancer_id, client_id, name, counts_open, counts_done, allows_delete, participants_can_move, position, system_key
)
SELECT p.freelancer_id, p.client_id, v.name, v.counts_open, v.counts_done, v.allows_delete, v.participants_can_move, v.position, v.system_key
FROM (SELECT DISTINCT freelancer_id, client_id FROM workflow_statuses) p
CROSS JOIN (
    VALUES
        ('new'::text, 'New'::text, true, false, true, true, 0),
        ('in_progress', 'In progress', true, false, false, true, 1),
        ('done', 'Done', false, true, false, false, 2),
        ('cancelled', 'Cancelled', false, false, false, true, 3)
) AS v(system_key, name, counts_open, counts_done, allows_delete, participants_can_move, position);

ALTER TABLE workflow_statuses
    ADD COLUMN category_id UUID REFERENCES workflow_categories (id);

UPDATE workflow_statuses ws
SET category_id = c.id
FROM workflow_categories c
WHERE c.freelancer_id = ws.freelancer_id
  AND c.client_id IS NOT DISTINCT FROM ws.client_id
  AND c.system_key = ws.category;

ALTER TABLE workflow_statuses ALTER COLUMN category_id SET NOT NULL;

CREATE INDEX workflow_statuses_category_idx ON workflow_statuses (category_id);

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
