-- When a task was completed: set when it enters a "done" column, cleared when
-- it leaves. Tasks already done take their last change as the best guess.
ALTER TABLE tasks ADD COLUMN completed_at TIMESTAMPTZ;
UPDATE tasks SET completed_at = updated_at WHERE status = 'done';

-- A freelancer's report of the work done for a client over a period. Its tasks
-- are snapshots taken when the report is made, so a sent report doesn't change
-- when the tasks do. Drafts are the freelancer's alone; the client sees a
-- report once it's sent.
CREATE TABLE reports (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    freelancer_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    client_id     UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    project_id    UUID REFERENCES projects (id) ON DELETE SET NULL,
    project_name  TEXT NOT NULL DEFAULT '',
    title         TEXT NOT NULL,
    summary       TEXT NOT NULL DEFAULT '',
    period_from   DATE NOT NULL,
    period_to     DATE NOT NULL,
    status        TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'sent')),
    sent_at       TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (period_from <= period_to)
);

CREATE INDEX reports_freelancer_idx ON reports (freelancer_id, created_at DESC);
CREATE INDEX reports_client_idx ON reports (client_id, sent_at DESC);

CREATE TRIGGER reports_set_updated_at
    BEFORE UPDATE ON reports
    FOR EACH ROW
    EXECUTE FUNCTION set_updated_at();

CREATE TABLE report_items (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    report_id    UUID NOT NULL REFERENCES reports (id) ON DELETE CASCADE,
    task_id      UUID REFERENCES tasks (id) ON DELETE SET NULL,
    title        TEXT NOT NULL,
    project_name TEXT NOT NULL DEFAULT '',
    status_name  TEXT NOT NULL DEFAULT '',
    completed_on DATE NOT NULL,
    position     INT NOT NULL
);

CREATE INDEX report_items_report_idx ON report_items (report_id, position);

-- Notifications can point at a report ("… sent you a report").
ALTER TABLE notifications ADD COLUMN report_id UUID REFERENCES reports (id) ON DELETE SET NULL;
