-- Admins can also end one of an account's sessions, and lift a rate limit
-- that holds someone back. A limit on an address has no account: its
-- target_email is the address.
ALTER TABLE admin_actions DROP CONSTRAINT admin_actions_action_check;
ALTER TABLE admin_actions ADD CONSTRAINT admin_actions_action_check
    CHECK (action IN ('block', 'unblock', 'end_sessions', 'reset_two_factor', 'retry_email', 'resend_signup', 'delete_signup', 'end_session', 'clear_limit'));
