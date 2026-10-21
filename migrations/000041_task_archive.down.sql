-- Archived tasks come back onto their boards, in the columns they were moved
-- to; the Paid columns are not made again.
DROP INDEX tasks_archived_idx;
ALTER TABLE tasks DROP COLUMN archived_report_id, DROP COLUMN archived_at;
