-- A sent report can be marked paid by its client; its tasks then move to the
-- pair's "Paid" column (a status of the done category).
ALTER TABLE reports DROP CONSTRAINT reports_status_check;
ALTER TABLE reports ADD CONSTRAINT reports_status_check CHECK (status IN ('draft', 'sent', 'paid'));
ALTER TABLE reports ADD COLUMN paid_at TIMESTAMPTZ;

-- A report's history: who did what to it and when. Data holds the change,
-- e.g. {"from": "Oct", "to": "October"} or {"task": "Logo"}.
CREATE TABLE report_events (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    report_id  UUID NOT NULL REFERENCES reports (id) ON DELETE CASCADE,
    actor_id   UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind       TEXT NOT NULL,
    data       JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX report_events_report_idx ON report_events (report_id, created_at);

-- Reports from before history start with their creation, and their sending.
INSERT INTO report_events (report_id, actor_id, kind, created_at)
SELECT id, freelancer_id, 'created', created_at FROM reports;
INSERT INTO report_events (report_id, actor_id, kind, created_at)
SELECT id, freelancer_id, 'sent', sent_at FROM reports WHERE sent_at IS NOT NULL;
