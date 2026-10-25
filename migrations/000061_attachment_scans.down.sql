DROP INDEX attachments_to_scan_idx;
ALTER TABLE attachments
    DROP COLUMN scan_status,
    DROP COLUMN threat,
    DROP COLUMN scan_claimed_at,
    DROP COLUMN scanned_at;
