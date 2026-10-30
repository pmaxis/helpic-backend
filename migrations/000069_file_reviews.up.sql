-- A file on a task can have versions: a new mockup uploaded in place of the
-- one before it. Versions share a series (the first version's ID) and count
-- up from 1; every version stays, the latest is the one shown.
ALTER TABLE attachments
    ADD COLUMN series_id UUID,
    ADD COLUMN version   INT NOT NULL DEFAULT 1 CHECK (version >= 1);
UPDATE attachments SET series_id = id;
ALTER TABLE attachments ALTER COLUMN series_id SET NOT NULL;
CREATE UNIQUE INDEX attachments_series_version_idx ON attachments (series_id, version);

-- The client's side says what they think of a version: approved, or that it
-- needs changes. NULL while nobody has; a new version starts without one.
ALTER TABLE attachments
    ADD COLUMN review      TEXT CHECK (review IN ('approved', 'changes_requested')),
    ADD COLUMN reviewed_by UUID REFERENCES users (id) ON DELETE SET NULL,
    ADD COLUMN reviewed_at TIMESTAMPTZ;

-- A comment can point at a spot on an image on its task: x and y are
-- fractions of its width and height from the top left corner. Once the
-- image is deleted the comment stays, pointing at nothing.
ALTER TABLE comments
    ADD COLUMN attachment_id UUID REFERENCES attachments (id) ON DELETE SET NULL,
    ADD COLUMN pin_x         REAL CHECK (pin_x BETWEEN 0 AND 1),
    ADD COLUMN pin_y         REAL CHECK (pin_y BETWEEN 0 AND 1),
    ADD CONSTRAINT comments_pin_check CHECK ((pin_x IS NULL) = (pin_y IS NULL));
CREATE INDEX comments_attachment_idx ON comments (attachment_id) WHERE attachment_id IS NOT NULL;

-- Both new kinds are on wherever a user already chose kinds, as the
-- defaults have them for everyone else. A channel turned off stays off.
UPDATE telegram_links SET kinds = kinds || ARRAY['file_approved', 'file_changes_requested']
WHERE cardinality(kinds) > 0 AND NOT 'file_approved' = ANY (kinds);
UPDATE email_notification_prefs SET kinds = kinds || ARRAY['file_approved', 'file_changes_requested']
WHERE cardinality(kinds) > 0 AND NOT 'file_approved' = ANY (kinds);
UPDATE app_notification_prefs SET kinds = kinds || ARRAY['file_approved', 'file_changes_requested']
WHERE cardinality(kinds) > 0 AND NOT 'file_approved' = ANY (kinds);
