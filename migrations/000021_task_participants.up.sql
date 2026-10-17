-- Who took part in a task, kept as it happens instead of worked out on every
-- read: its freelancer and creator, and everyone who comments, attaches a
-- file or changes it. first_at is when they first did; leaving (a deleted
-- comment) doesn't take anyone off.
CREATE TABLE task_participants (
    task_id  UUID NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
    user_id  UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    first_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (task_id, user_id)
);

-- Notes that the user took part, keeping the earliest time.
CREATE FUNCTION note_participant(t UUID, u UUID, at TIMESTAMPTZ) RETURNS void LANGUAGE sql AS $$
    INSERT INTO task_participants (task_id, user_id, first_at) VALUES (t, u, at)
    ON CONFLICT (task_id, user_id) DO UPDATE SET first_at = LEAST(task_participants.first_at, EXCLUDED.first_at)
$$;

CREATE FUNCTION task_participants_on_task() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    PERFORM note_participant(NEW.id, NEW.freelancer_id, NEW.created_at);
    IF NEW.created_by IS NOT NULL THEN
        PERFORM note_participant(NEW.id, NEW.created_by, NEW.created_at);
    END IF;
    RETURN NULL;
END;
$$;

CREATE FUNCTION task_participants_on_act() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    -- TG_ARGV[0] names the column holding who did it.
    PERFORM note_participant(NEW.task_id, (to_jsonb(NEW) ->> TG_ARGV[0])::uuid, NEW.created_at);
    RETURN NULL;
END;
$$;

CREATE TRIGGER tasks_note_participants AFTER INSERT ON tasks
    FOR EACH ROW EXECUTE FUNCTION task_participants_on_task();
CREATE TRIGGER comments_note_participant AFTER INSERT ON comments
    FOR EACH ROW EXECUTE FUNCTION task_participants_on_act('author_id');
CREATE TRIGGER attachments_note_participant AFTER INSERT ON attachments
    FOR EACH ROW EXECUTE FUNCTION task_participants_on_act('uploaded_by');
CREATE TRIGGER task_events_note_participant AFTER INSERT ON task_events
    FOR EACH ROW EXECUTE FUNCTION task_participants_on_act('actor_id');

-- Everyone who already took part.
INSERT INTO task_participants (task_id, user_id, first_at)
SELECT task_id, who, min(at) FROM (
    SELECT id AS task_id, freelancer_id AS who, created_at AS at FROM tasks
    UNION ALL SELECT id, created_by, created_at FROM tasks WHERE created_by IS NOT NULL
    UNION ALL SELECT task_id, author_id, created_at FROM comments
    UNION ALL SELECT task_id, uploaded_by, created_at FROM attachments
    UNION ALL SELECT task_id, actor_id, created_at FROM task_events
) acts GROUP BY task_id, who;
