-- The bell is a channel like email and Telegram: the user picks which kinds
-- show there; without a row, all of them. A notification is still kept
-- whatever they pick, for the other channels; in_app says whether the bell
-- shows it, as the choice was when it happened.
CREATE TABLE app_notification_prefs (
    user_id    UUID PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    kinds      TEXT[] NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE notifications ADD COLUMN in_app BOOLEAN NOT NULL DEFAULT true;
