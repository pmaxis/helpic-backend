-- A project groups tasks. Like a task, it is shared between a linked freelancer
-- and client, or is a freelancer's personal project with no client.
CREATE TABLE projects (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    freelancer_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    client_id     UUID REFERENCES users (id) ON DELETE CASCADE,
    created_by    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    description   TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT projects_personal_by_freelancer CHECK (client_id IS NOT NULL OR created_by = freelancer_id)
);

CREATE INDEX projects_freelancer_idx ON projects (freelancer_id);
CREATE INDEX projects_client_idx ON projects (client_id);

CREATE TRIGGER projects_set_updated_at
    BEFORE UPDATE ON projects
    FOR EACH ROW
    EXECUTE FUNCTION set_updated_at();

-- Deleting a project keeps its tasks; they just leave the project.
ALTER TABLE tasks ADD COLUMN project_id UUID REFERENCES projects (id) ON DELETE SET NULL;
CREATE INDEX tasks_project_idx ON tasks (project_id);
