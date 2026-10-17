ALTER TABLE tasks DROP COLUMN IF EXISTS position, DROP COLUMN IF EXISTS status_id;
DROP FUNCTION IF EXISTS ensure_workflow(UUID, UUID);
DROP TABLE IF EXISTS workflow_statuses;

CREATE TABLE board_columns (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    status     TEXT NOT NULL CHECK (status IN ('new', 'in_progress', 'done', 'cancelled')),
    position   INT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX board_columns_user_idx ON board_columns (user_id, position);
CREATE TABLE task_placements (
    user_id   UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    task_id   UUID NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
    column_id UUID NOT NULL REFERENCES board_columns (id) ON DELETE CASCADE,
    position  INT NOT NULL,
    PRIMARY KEY (user_id, task_id)
);
