-- Every public sign-up is a freelancer; client accounts are created only by
-- accepting a freelancer's invitation.
ALTER TABLE users
    ADD COLUMN name TEXT NOT NULL DEFAULT '',
    ADD COLUMN role TEXT NOT NULL DEFAULT 'freelancer' CHECK (role IN ('freelancer', 'client'));

CREATE TABLE invitations (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    freelancer_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    email         TEXT NOT NULL,
    name          TEXT NOT NULL DEFAULT '',
    token         TEXT NOT NULL,
    expires_at    TIMESTAMPTZ NOT NULL,
    accepted_at   TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX invitations_token_key ON invitations (token);
-- One open invitation per email per freelancer.
CREATE UNIQUE INDEX invitations_pending_key ON invitations (freelancer_id, email) WHERE accepted_at IS NULL;

-- A client may work with several freelancers and the other way round.
CREATE TABLE freelancer_clients (
    freelancer_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    client_id     UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (freelancer_id, client_id)
);

CREATE INDEX freelancer_clients_client_idx ON freelancer_clients (client_id);

CREATE TABLE tasks (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    freelancer_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    client_id     UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    title         TEXT NOT NULL,
    description   TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL DEFAULT 'new' CHECK (status IN ('new', 'in_progress', 'done', 'cancelled')),
    due_on        DATE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX tasks_freelancer_idx ON tasks (freelancer_id, created_at DESC);
CREATE INDEX tasks_client_idx ON tasks (client_id, created_at DESC);

CREATE TRIGGER tasks_set_updated_at
    BEFORE UPDATE ON tasks
    FOR EACH ROW
    EXECUTE FUNCTION set_updated_at();
