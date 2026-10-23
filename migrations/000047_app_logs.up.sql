-- The server's warnings and errors, for the admin panel: failed requests,
-- panics, background jobs and mail that didn't go out. Kept 30 days. The
-- request's path is not stored, only its route, so tokens in links stay out.
CREATE TABLE app_logs (
    id      BIGSERIAL PRIMARY KEY,
    at      TIMESTAMPTZ NOT NULL,
    level   TEXT NOT NULL CHECK (level IN ('WARN', 'ERROR')),
    message TEXT NOT NULL,
    attrs   JSONB NOT NULL DEFAULT '{}'
);

CREATE INDEX app_logs_at_idx ON app_logs (at);
CREATE INDEX app_logs_level_id_idx ON app_logs (level, id DESC);
