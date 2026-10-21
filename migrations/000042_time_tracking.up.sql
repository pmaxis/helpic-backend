-- Time a freelancer spends on a task: written in by hand, or counted by a
-- timer. A running entry has running_since set; stopping it adds the time
-- since then to seconds. A user runs at most one timer at a time.
CREATE TABLE time_entries (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id       UUID NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
    user_id       UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    spent_on      DATE NOT NULL,
    seconds       INT NOT NULL DEFAULT 0 CHECK (seconds >= 0),
    note          TEXT NOT NULL DEFAULT '',
    running_since TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX time_entries_task_idx ON time_entries (task_id, spent_on DESC, created_at DESC);
CREATE UNIQUE INDEX time_entries_one_timer ON time_entries (user_id) WHERE running_since IS NOT NULL;

CREATE TRIGGER time_entries_set_updated_at
    BEFORE UPDATE ON time_entries
    FOR EACH ROW
    EXECUTE FUNCTION set_updated_at();

-- A freelancer's hourly rate, in cents of their currency, and on a project
-- the rate they set for it instead. NULL = none.
ALTER TABLE users ADD COLUMN hourly_rate_cents BIGINT CHECK (hourly_rate_cents >= 0);
ALTER TABLE project_participants ADD COLUMN hourly_rate_cents BIGINT CHECK (hourly_rate_cents >= 0);

-- A task is billed at a fixed price, or by the hour: then its price is its
-- time at its freelancer's rate (the project's, or their own), kept up to
-- date below, so everything that reads price_cents bills the hours too.
ALTER TABLE tasks ADD COLUMN billing TEXT NOT NULL DEFAULT 'fixed' CHECK (billing IN ('fixed', 'hourly'));

-- The time written on a task so far; NULL when there is none.
CREATE FUNCTION task_seconds(task UUID) RETURNS BIGINT LANGUAGE sql STABLE AS $$
    SELECT sum(seconds) FROM time_entries WHERE task_id = task
$$;

-- What the time on a task comes to at its freelancer's rate, billed by the
-- whole minute (as the time is shown); NULL without time or a rate.
CREATE FUNCTION hourly_price(task UUID, freelancer UUID, project UUID) RETURNS BIGINT LANGUAGE sql STABLE AS $$
    SELECT round(round(task_seconds(task) / 60.0) * coalesce(
        (SELECT pp.hourly_rate_cents FROM project_participants pp WHERE pp.project_id = project AND pp.user_id = freelancer),
        (SELECT u.hourly_rate_cents FROM users u WHERE u.id = freelancer)
    ) / 60.0)::bigint
$$;

-- Whether a task is on a report that was sent: its price and time are billed.
CREATE FUNCTION task_billed(task UUID) RETURNS boolean LANGUAGE sql STABLE AS $$
    SELECT EXISTS (
        SELECT 1 FROM report_items i JOIN reports r ON r.id = i.report_id
        WHERE i.task_id = task AND r.status <> 'draft'
    )
$$;

-- An hourly task's price follows its time, freelancer and project until it's
-- billed on a sent report; then it stays as it was billed.
CREATE FUNCTION tasks_hourly_price() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.billing = 'hourly' AND NEW.archived_at IS NULL AND NOT task_billed(NEW.id) THEN
        NEW.price_cents := hourly_price(NEW.id, NEW.freelancer_id, NEW.project_id);
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER tasks_hourly_price BEFORE INSERT OR UPDATE ON tasks
    FOR EACH ROW EXECUTE FUNCTION tasks_hourly_price();

-- Touching an hourly task runs the trigger above, which prices it again.
CREATE FUNCTION reprice_hourly_tasks() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF TG_TABLE_NAME = 'time_entries' THEN
        UPDATE tasks SET billing = billing
        WHERE id IN (OLD.task_id, NEW.task_id) AND billing = 'hourly' AND archived_at IS NULL;
    ELSIF TG_TABLE_NAME = 'users' THEN
        UPDATE tasks SET billing = billing
        WHERE freelancer_id = NEW.id AND billing = 'hourly' AND archived_at IS NULL;
    ELSE
        UPDATE tasks SET billing = billing
        WHERE project_id = coalesce(NEW.project_id, OLD.project_id) AND freelancer_id = coalesce(NEW.user_id, OLD.user_id)
          AND billing = 'hourly' AND archived_at IS NULL;
    END IF;
    RETURN NULL;
END;
$$;

-- A running timer's seconds don't change until it stops, so its start alone
-- changes no price.
CREATE TRIGGER time_entries_reprice AFTER INSERT OR UPDATE OF seconds, task_id OR DELETE ON time_entries
    FOR EACH ROW EXECUTE FUNCTION reprice_hourly_tasks();
CREATE TRIGGER users_reprice AFTER UPDATE OF hourly_rate_cents ON users
    FOR EACH ROW EXECUTE FUNCTION reprice_hourly_tasks();
CREATE TRIGGER project_participants_reprice AFTER UPDATE OF hourly_rate_cents OR DELETE ON project_participants
    FOR EACH ROW EXECUTE FUNCTION reprice_hourly_tasks();

-- A report shows an hourly task's time beside its price: live while it's a
-- draft, frozen with the price when it's sent. NULL for a fixed-price task.
ALTER TABLE report_items ADD COLUMN seconds BIGINT;
