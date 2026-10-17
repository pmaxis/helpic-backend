DELETE FROM reports WHERE client_id IS NULL;
ALTER TABLE reports DROP CONSTRAINT IF EXISTS reports_personal_not_sent;
ALTER TABLE reports ALTER COLUMN client_id SET NOT NULL;
