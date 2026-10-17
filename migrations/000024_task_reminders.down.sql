UPDATE telegram_links SET kinds = array_remove(array_remove(kinds, 'task_due_soon'), 'task_overdue');
DELETE FROM notifications WHERE actor_id IS NULL;
ALTER TABLE notifications ALTER COLUMN actor_id SET NOT NULL;
DROP INDEX tasks_open_due_idx;
DROP TABLE task_reminders;
