-- A report without a client covers a freelancer's personal work: made to be
-- downloaded, never sent.
ALTER TABLE reports ALTER COLUMN client_id DROP NOT NULL;
ALTER TABLE reports ADD CONSTRAINT reports_personal_not_sent CHECK (client_id IS NOT NULL OR status = 'draft');
