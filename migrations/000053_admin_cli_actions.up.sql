-- What the server's cli does to admin accounts, recorded like an admin's
-- actions: admin_id is NULL, admin_email names the cli.
ALTER TABLE admin_actions DROP CONSTRAINT admin_actions_action_check;
ALTER TABLE admin_actions ADD CONSTRAINT admin_actions_action_check
    CHECK (action IN ('block', 'unblock', 'end_sessions', 'reset_two_factor', 'retry_email', 'resend_signup', 'delete_signup', 'end_session', 'clear_limit',
                      'lock_admin', 'unlock_admin', 'reset_admin'));
