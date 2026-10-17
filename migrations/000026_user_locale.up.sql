-- The language a user reads Helpic in: the interface, and the email and
-- Telegram messages that reach them. Set from the browser's language when the
-- account is made; they can change it.
ALTER TABLE users ADD COLUMN locale TEXT NOT NULL DEFAULT 'en' CHECK (locale IN ('en', 'uk'));
