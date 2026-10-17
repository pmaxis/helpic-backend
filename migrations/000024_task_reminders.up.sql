-- Reminders about a task's due date, kept so each goes out once: 'due_soon'
-- the day before, 'overdue' the day after. Claiming a row is what sends it,
-- so two runs (or two servers) can't both send one. A new due date is a new
-- reminder.
CREATE TABLE task_reminders (
    task_id UUID NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
    kind    TEXT NOT NULL CHECK (kind IN ('due_soon', 'overdue')),
    due_on  DATE NOT NULL,
    sent_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (task_id, kind, due_on)
);

-- Finding the open tasks due on a day.
CREATE INDEX tasks_open_due_idx ON tasks (due_on) WHERE status IN ('new', 'in_progress');

-- A reminder has no actor: nobody did anything, the date came.
ALTER TABLE notifications ALTER COLUMN actor_id DROP NOT NULL;

-- Deadlines are important: chats already connected to Telegram get them too.
UPDATE telegram_links SET kinds = kinds || ARRAY['task_due_soon', 'task_overdue'];
