-- A basic report lists the tasks; a detailed one also says what was done in
-- each, in a note the freelancer writes (it starts as the task's description).
ALTER TABLE reports ADD COLUMN kind TEXT NOT NULL DEFAULT 'basic' CHECK (kind IN ('basic', 'detailed'));
ALTER TABLE report_items ADD COLUMN notes TEXT NOT NULL DEFAULT '';
