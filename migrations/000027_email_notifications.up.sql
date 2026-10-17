-- Notifications also go by email, a few minutes late so that what happens
-- together arrives in one email, and not at all once read in the app.
-- emailed_at is when a notification was dealt with for email (sent, or
-- skipped); the ones already here are old news.
ALTER TABLE notifications ADD COLUMN emailed_at TIMESTAMPTZ;
UPDATE notifications SET emailed_at = created_at;
CREATE INDEX notifications_unemailed_idx ON notifications (created_at) WHERE emailed_at IS NULL;

-- The notification kinds a user wants by email; without a row, the defaults.
CREATE TABLE email_notification_prefs (
    user_id    UUID PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    kinds      TEXT[] NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
