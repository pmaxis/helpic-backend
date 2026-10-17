-- A user's chat with the Telegram bot, where their notifications also go.
-- kinds are the notification kinds they want there; username is their
-- Telegram handle (or first name), to show which account is connected.
CREATE TABLE telegram_links (
    user_id   UUID PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    chat_id   BIGINT NOT NULL,
    username  TEXT NOT NULL DEFAULT '',
    kinds     TEXT[] NOT NULL,
    linked_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX telegram_links_chat_idx ON telegram_links (chat_id);

-- A one-time code in the bot's /start link: whoever opens it in Telegram
-- connects their chat to the user who asked for it. Only its SHA-256 is kept.
CREATE TABLE telegram_link_codes (
    code_hash  BYTEA PRIMARY KEY,
    user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL
);
