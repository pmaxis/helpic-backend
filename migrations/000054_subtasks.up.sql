-- A task can be split into subtasks: tasks of their own, with their own
-- status, freelancer, price and time, under a parent task. One level deep: a
-- subtask has no subtasks. A subtask is with its parent's client, and in its
-- project (the service moves them together). Deleting a parent takes its
-- subtasks along; the service only deletes a task that has none.
ALTER TABLE tasks ADD COLUMN parent_id UUID REFERENCES tasks (id) ON DELETE CASCADE;

CREATE INDEX tasks_parent_idx ON tasks (parent_id, created_at, id) WHERE parent_id IS NOT NULL;

CREATE FUNCTION tasks_check_parent() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    p tasks%ROWTYPE;
BEGIN
    IF NEW.parent_id IS NULL THEN
        RETURN NEW;
    END IF;
    SELECT * INTO p FROM tasks WHERE id = NEW.parent_id;
    IF p.parent_id IS NOT NULL OR EXISTS (SELECT 1 FROM tasks WHERE parent_id = NEW.id) THEN
        RAISE EXCEPTION 'subtasks are one level deep';
    END IF;
    IF p.client_id IS DISTINCT FROM NEW.client_id THEN
        RAISE EXCEPTION 'a subtask is with its parent''s client';
    END IF;
    RETURN NEW;
END $$;

CREATE TRIGGER tasks_check_parent BEFORE INSERT OR UPDATE OF parent_id, client_id ON tasks
    FOR EACH ROW EXECUTE FUNCTION tasks_check_parent();

-- A report names the parent of a subtask it lists, as it was then.
ALTER TABLE report_items ADD COLUMN parent_title TEXT NOT NULL DEFAULT '';
