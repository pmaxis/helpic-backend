-- A company's people are members, who see all the company's work, or
-- contractors, who see only the tasks they set or were added to. The owner
-- picks the role when inviting and can change it later.
ALTER TABLE company_members ADD COLUMN role TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('member', 'contractor'));
ALTER TABLE company_invites ADD COLUMN role TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('member', 'contractor'));

CREATE FUNCTION is_contractor(u UUID) RETURNS boolean LANGUAGE sql STABLE AS $$
    SELECT EXISTS (SELECT 1 FROM company_members WHERE user_id = u AND role = 'contractor')
$$;

-- Whether the user sees a task: its freelancer does; on the client's side the
-- company's owner and members do, and a contractor only once they take part
-- in it (task_participants: they set it, were added, or acted on it).
CREATE FUNCTION sees_task(u UUID, task UUID, freelancer UUID, client UUID) RETURNS boolean LANGUAGE sql STABLE AS $$
    SELECT freelancer = u
        OR (client = acting_client(u)
            AND (NOT is_contractor(u)
                 OR EXISTS (SELECT 1 FROM task_participants tp WHERE tp.task_id = task AND tp.user_id = u)))
$$;
