-- People can delete their account, and admins can delete one for them. The
-- row stays, so the work it shares with others keeps its author: the tasks,
-- comments, files and reports the other side still needs. What identifies the
-- person goes: the email is emptied, the name replaced, the password and
-- two-factor secrets wiped, and their sessions, links and settings deleted.
ALTER TABLE users ADD COLUMN deleted_at TIMESTAMPTZ;

-- A deleted account's email is empty, so the address is free for a new one.
DROP INDEX users_email_key;
CREATE UNIQUE INDEX users_email_key ON users (email) WHERE deleted_at IS NULL;

ALTER TABLE admin_actions DROP CONSTRAINT admin_actions_action_check;
ALTER TABLE admin_actions ADD CONSTRAINT admin_actions_action_check
    CHECK (action IN ('block', 'unblock', 'end_sessions', 'reset_two_factor', 'retry_email', 'resend_signup', 'delete_signup', 'end_session', 'clear_limit',
                      'lock_admin', 'unlock_admin', 'reset_admin', 'delete'));
