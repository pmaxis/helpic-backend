-- A task's history: who changed what and when. Data holds the change, e.g.
-- {"from": "New", "to": "In progress"}, with names as they were at the time.
CREATE TABLE task_events (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id    UUID NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
    actor_id   UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind       TEXT NOT NULL,
    data       JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX task_events_task_idx ON task_events (task_id, created_at);

-- Tasks made before history existed start with their creation.
INSERT INTO task_events (task_id, actor_id, kind, created_at)
SELECT id, coalesce(created_by, freelancer_id), 'created', created_at FROM tasks;
