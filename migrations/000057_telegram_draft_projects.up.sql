-- A task from a message may go into a project: once its sender picks who
-- it's with, the bot offers that pair's projects. choice is who they picked
-- ("p" for personal, or a target's place), projects the projects offered,
-- in the order of their buttons: [{"id", "name"}].
ALTER TABLE telegram_drafts
    ADD COLUMN choice TEXT NOT NULL DEFAULT '',
    ADD COLUMN projects JSONB NOT NULL DEFAULT '[]';
