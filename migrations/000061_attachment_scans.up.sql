-- Whether ClamAV has checked a file. 'pending' waits for the check and can't
-- be downloaded yet; 'infected' had malware: its bytes are gone, and threat
-- names what was found. 'unscanned' was stored while no scanner was set up:
-- it downloads, and is checked once there is one. Files already here are
-- 'unscanned'.
ALTER TABLE attachments
    ADD COLUMN scan_status     TEXT NOT NULL DEFAULT 'unscanned'
        CHECK (scan_status IN ('unscanned', 'pending', 'clean', 'infected')),
    ADD COLUMN threat          TEXT NOT NULL DEFAULT '',
    -- When a server took the file to check; another takes it only once this
    -- is old, in case the first stopped halfway.
    ADD COLUMN scan_claimed_at TIMESTAMPTZ,
    ADD COLUMN scanned_at      TIMESTAMPTZ;

CREATE INDEX attachments_to_scan_idx ON attachments (created_at) WHERE scan_status IN ('pending', 'unscanned');
