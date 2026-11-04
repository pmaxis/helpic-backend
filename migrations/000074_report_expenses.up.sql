-- A report can bill what the work cost: hosting, licences, a subcontractor.
-- An expense is a line of its own, written in the draft: no task behind it,
-- so its title, day and amount are the line's own, in the report's currency,
-- and its amount is kept as it is, not read from a task.
ALTER TABLE report_items ADD COLUMN expense BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE report_items ADD CONSTRAINT report_items_expense_no_task CHECK (NOT expense OR task_id IS NULL);
