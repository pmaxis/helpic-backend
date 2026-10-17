DROP TABLE report_events;
ALTER TABLE reports DROP COLUMN paid_at;
UPDATE reports SET status = 'sent' WHERE status = 'paid';
ALTER TABLE reports DROP CONSTRAINT reports_status_check;
ALTER TABLE reports ADD CONSTRAINT reports_status_check CHECK (status IN ('draft', 'sent'));
