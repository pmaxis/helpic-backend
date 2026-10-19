-- A task is on at most one report. Removing it from a draft frees it;
-- a sent or paid report keeps it, so the same work is not billed twice.
-- If a task was snapshotted twice already, the earlier report keeps it.
DELETE FROM report_items
WHERE id IN (
    SELECT id FROM (
        SELECT i.id, row_number() OVER (PARTITION BY i.task_id ORDER BY r.created_at, i.position, i.id) AS n
        FROM report_items i
        JOIN reports r ON r.id = i.report_id
        WHERE i.task_id IS NOT NULL
    ) ranked
    WHERE n > 1
);

CREATE UNIQUE INDEX report_items_one_task ON report_items (task_id) WHERE task_id IS NOT NULL;

-- Sign-in limits shared by every copy of the API. One row is one bucket.
CREATE TABLE rate_limit_buckets (
    scope         TEXT NOT NULL,
    key           TEXT NOT NULL,
    tokens        DOUBLE PRECISION NOT NULL,
    updated_at    TIMESTAMPTZ NOT NULL,
    burst         DOUBLE PRECISION NOT NULL,
    every_seconds DOUBLE PRECISION NOT NULL,
    PRIMARY KEY (scope, key)
);
