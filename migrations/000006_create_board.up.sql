-- Every user arranges tasks on their own kanban board. Each column stands for
-- one task status; several columns may share a status.
CREATE TABLE board_columns (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    status     TEXT NOT NULL CHECK (status IN ('new', 'in_progress', 'done', 'cancelled')),
    position   INT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX board_columns_user_idx ON board_columns (user_id, position);

-- Where a user put a task on their board. A placement whose column no longer
-- matches the task's status is ignored, and the task shows in the first column
-- of its status instead.
CREATE TABLE task_placements (
    user_id   UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    task_id   UUID NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
    column_id UUID NOT NULL REFERENCES board_columns (id) ON DELETE CASCADE,
    position  INT NOT NULL,
    PRIMARY KEY (user_id, task_id)
);
