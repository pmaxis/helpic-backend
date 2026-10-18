-- The project's author decides who takes part. Only the author starts as a
-- participant; they invite the freelancer, the client, an employee or a
-- contractor. Seeing a project is being one of its participants.
CREATE TABLE project_participants (
    project_id UUID NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    invited_by UUID REFERENCES users (id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, user_id)
);

INSERT INTO project_participants (project_id, user_id, invited_by)
SELECT id, created_by, created_by FROM projects
ON CONFLICT DO NOTHING;

CREATE FUNCTION sees_project(u UUID, project UUID) RETURNS boolean LANGUAGE sql STABLE AS $$
    SELECT EXISTS (
        SELECT 1 FROM project_participants
        WHERE project_id = project AND user_id = u
    )
$$;
