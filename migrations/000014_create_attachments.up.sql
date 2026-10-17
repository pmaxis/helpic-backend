-- Files attached to a task: mockups, specs, results. The bytes live on disk
-- (UPLOADS_DIR/<id>); this row says what they are and who added them.
CREATE TABLE attachments (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id      UUID NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
    uploaded_by  UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    file_name    TEXT NOT NULL,
    content_type TEXT NOT NULL,
    size_bytes   BIGINT NOT NULL CHECK (size_bytes >= 0),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX attachments_task_idx ON attachments (task_id, created_at);
