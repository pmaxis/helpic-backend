DROP TRIGGER tasks_leave_backlog ON tasks;
DROP FUNCTION tasks_leave_backlog();
DELETE FROM task_events WHERE kind = 'backlog';
ALTER TABLE tasks DROP COLUMN in_backlog;
