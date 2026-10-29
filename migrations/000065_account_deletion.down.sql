DELETE FROM admin_actions WHERE action = 'delete';
ALTER TABLE admin_actions DROP CONSTRAINT admin_actions_action_check;
ALTER TABLE admin_actions ADD CONSTRAINT admin_actions_action_check
    CHECK (action IN ('block', 'unblock', 'end_sessions', 'reset_two_factor', 'retry_email', 'resend_signup', 'delete_signup', 'end_session', 'clear_limit',
                      'lock_admin', 'unlock_admin', 'reset_admin'));

-- Deleted accounts keep a unique address that no one can sign in with.
UPDATE users SET email = 'deleted-' || id || '@helpic.invalid' WHERE deleted_at IS NOT NULL;
DROP INDEX users_email_key;
CREATE UNIQUE INDEX users_email_key ON users (email);
ALTER TABLE users DROP COLUMN deleted_at;
