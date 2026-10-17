-- A client (the one a freelancer invited) can have a company and bring in
-- its people — employees or contractors, one "member" role for now. Members
-- act for the company: they see its freelancers, tasks, projects and boards,
-- and set tasks for its freelancers. Reports stay the owner's.
CREATE TABLE companies (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id   UUID NOT NULL UNIQUE REFERENCES users (id) ON DELETE CASCADE,
    name       TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One company per member.
CREATE TABLE company_members (
    user_id    UUID PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    company_id UUID NOT NULL REFERENCES companies (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX company_members_company_idx ON company_members (company_id, created_at);

CREATE TABLE company_invites (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id  UUID NOT NULL REFERENCES companies (id) ON DELETE CASCADE,
    email       TEXT NOT NULL,
    name        TEXT NOT NULL DEFAULT '',
    token       TEXT NOT NULL UNIQUE,
    expires_at  TIMESTAMPTZ NOT NULL,
    accepted_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One open invitation per email per company.
CREATE UNIQUE INDEX company_invites_pending_key ON company_invites (company_id, email) WHERE accepted_at IS NULL;

-- The client a user acts as: their company's owner for a member, themselves
-- for everyone else. Queries that ask "is this user the task's client?" ask
-- "client_id = acting_client(user)".
CREATE FUNCTION acting_client(u UUID) RETURNS UUID LANGUAGE sql STABLE AS $$
    SELECT coalesce(
        (SELECT c.owner_id FROM company_members m JOIN companies c ON c.id = m.company_id WHERE m.user_id = u),
        u)
$$;
