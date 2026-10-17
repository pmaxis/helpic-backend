-- How urgent a task is; either side sets it.
ALTER TABLE tasks ADD COLUMN priority TEXT NOT NULL DEFAULT 'none'
    CHECK (priority IN ('none', 'low', 'medium', 'high', 'urgent'));

-- Labels belong to a freelancer–client pair (or a freelancer's personal work),
-- like the board's columns; both sides make them and put them on tasks.
CREATE TABLE labels (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    freelancer_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    client_id     UUID REFERENCES users (id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    color         TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One name per pair, whatever the case.
CREATE UNIQUE INDEX labels_pair_name_key
    ON labels (freelancer_id, coalesce(client_id, '00000000-0000-0000-0000-000000000000'::uuid), lower(name));

CREATE TABLE task_labels (
    task_id  UUID NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
    label_id UUID NOT NULL REFERENCES labels (id) ON DELETE CASCADE,
    PRIMARY KEY (task_id, label_id)
);

CREATE INDEX task_labels_label_idx ON task_labels (label_id);
