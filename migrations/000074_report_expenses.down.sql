DELETE FROM report_items WHERE expense;
ALTER TABLE report_items DROP CONSTRAINT report_items_expense_no_task;
ALTER TABLE report_items DROP COLUMN expense;
