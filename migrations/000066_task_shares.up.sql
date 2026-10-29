-- A task shared by a link: whoever opens it reads the task, its subtasks and
-- its files without an account, until the link is turned off. One link per
-- task; a new one is a new row.
--
-- The token is kept as it is, unlike sign-in and invitation links: it only
-- reads what this database already holds, so a copy of the database gives
-- nothing away through it, and the people who made it can copy it again.
CREATE TABLE task_shares (
    task_id    UUID PRIMARY KEY REFERENCES tasks (id) ON DELETE CASCADE,
    token      TEXT NOT NULL UNIQUE,
    created_by UUID NOT NULL REFERENCES users (id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
