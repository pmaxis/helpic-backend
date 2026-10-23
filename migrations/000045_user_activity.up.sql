-- The days each user used Helpic, in the app's time zone: one row per user
-- per day, written on their first signed-in request that day. It is what
-- daily, weekly and monthly active users are counted from in the admin
-- panel; sessions can't tell, they are deleted on sign-out.
CREATE TABLE user_activity (
    user_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    day     DATE NOT NULL,
    PRIMARY KEY (user_id, day)
);

CREATE INDEX user_activity_day_idx ON user_activity (day);

-- The past as far as it can be told: the days people signed up, worked on
-- tasks, commented, logged time or refreshed a session (UTC days).
INSERT INTO user_activity (user_id, day)
SELECT id, created_at::date FROM users
UNION SELECT actor_id, created_at::date FROM task_events
UNION SELECT author_id, created_at::date FROM comments
UNION SELECT user_id, created_at::date FROM time_entries
UNION SELECT user_id, last_used_at::date FROM sessions
ON CONFLICT DO NOTHING;
