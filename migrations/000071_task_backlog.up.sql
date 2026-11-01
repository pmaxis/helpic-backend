-- A task can wait in its board's backlog: it keeps its status, but sits off
-- the columns until someone puts it on the board. A done or archived task is
-- never in the backlog, however it got there.
ALTER TABLE tasks ADD COLUMN in_backlog BOOLEAN NOT NULL DEFAULT false;

CREATE FUNCTION tasks_leave_backlog() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status = 'done' OR NEW.archived_at IS NOT NULL THEN
        NEW.in_backlog := false;
    END IF;
    RETURN NEW;
END $$;

CREATE TRIGGER tasks_leave_backlog BEFORE INSERT OR UPDATE OF status, archived_at, in_backlog ON tasks
    FOR EACH ROW EXECUTE FUNCTION tasks_leave_backlog();
