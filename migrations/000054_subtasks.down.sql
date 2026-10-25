ALTER TABLE report_items DROP COLUMN parent_title;
DROP TRIGGER tasks_check_parent ON tasks;
DROP FUNCTION tasks_check_parent();
DROP INDEX tasks_parent_idx;
ALTER TABLE tasks DROP COLUMN parent_id;
