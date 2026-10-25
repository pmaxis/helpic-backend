-- The bot acts as well as tells. A message it sent is kept for a while: whose
-- it was and the task it was about, so a reply to it becomes a comment on
-- that task and its buttons act as that user.
CREATE TABLE telegram_messages (
    chat_id    BIGINT NOT NULL,
    message_id BIGINT NOT NULL,
    user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    task_id    UUID REFERENCES tasks (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chat_id, message_id)
);

CREATE INDEX telegram_messages_created_idx ON telegram_messages (created_at);

-- A task to be, from a message sent or forwarded to the bot, until its
-- sender picks who it's with. targets are the choices the bot offered, in
-- the order of its buttons: [{"id", "name"}], the id a client's for a
-- freelancer, a freelancer's for a client.
CREATE TABLE telegram_drafts (
    id          BIGSERIAL PRIMARY KEY,
    user_id     UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    title       TEXT NOT NULL,
    description TEXT NOT NULL,
    targets     JSONB NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
