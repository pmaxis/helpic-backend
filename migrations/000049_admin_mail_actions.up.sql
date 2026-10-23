-- Admins can also send a failed email again, and send a sign-up its link
-- again or remove it. A sign-up has no account yet: its target_id is NULL.
ALTER TABLE admin_actions DROP CONSTRAINT admin_actions_action_check;
ALTER TABLE admin_actions ADD CONSTRAINT admin_actions_action_check
    CHECK (action IN ('block', 'unblock', 'end_sessions', 'reset_two_factor', 'retry_email', 'resend_signup', 'delete_signup'));
