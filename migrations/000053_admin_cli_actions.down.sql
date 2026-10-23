DELETE FROM admin_actions WHERE action IN ('lock_admin', 'unlock_admin', 'reset_admin');
ALTER TABLE admin_actions DROP CONSTRAINT admin_actions_action_check;
ALTER TABLE admin_actions ADD CONSTRAINT admin_actions_action_check
    CHECK (action IN ('block', 'unblock', 'end_sessions', 'reset_two_factor', 'retry_email', 'resend_signup', 'delete_signup', 'end_session', 'clear_limit'));
