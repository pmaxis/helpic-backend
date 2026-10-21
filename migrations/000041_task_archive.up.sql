-- A task is archived once it's closed for good: its report was paid, or its
-- owner put a personal task away. An archived task is off every board and
-- can't be changed; it stays readable, and the archive lists it.
ALTER TABLE tasks
    ADD COLUMN archived_at TIMESTAMPTZ,
    ADD COLUMN archived_report_id UUID REFERENCES reports (id) ON DELETE SET NULL;

CREATE INDEX tasks_archived_idx ON tasks (archived_at DESC, id) WHERE archived_at IS NOT NULL;

-- Tasks of reports already paid are archived as of the payment.
UPDATE tasks t
SET archived_at = r.paid_at, archived_report_id = r.id
FROM report_items i
JOIN reports r ON r.id = i.report_id
WHERE i.task_id = t.id AND r.status = 'paid';

-- Payments used to move tasks into a Paid column. The archived ones go to
-- the board's other done column (or its first other column), and Paid
-- columns left empty go.
UPDATE tasks t
SET status_id = alt.id
FROM workflow_statuses paid
JOIN LATERAL (
    SELECT w.id FROM workflow_statuses w
    WHERE w.id <> paid.id
      AND w.freelancer_id = paid.freelancer_id
      AND w.client_id IS NOT DISTINCT FROM paid.client_id
      AND w.project_id IS NOT DISTINCT FROM paid.project_id
    ORDER BY (w.category = 'done') DESC, w.position
    LIMIT 1
) alt ON true
WHERE t.status_id = paid.id
  AND t.archived_at IS NOT NULL
  AND paid.category = 'done' AND lower(paid.name) = 'paid';

DELETE FROM workflow_statuses paid
WHERE paid.category = 'done' AND lower(paid.name) = 'paid'
  AND NOT EXISTS (SELECT 1 FROM tasks t WHERE t.status_id = paid.id);
