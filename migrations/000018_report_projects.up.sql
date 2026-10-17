-- A report covers any number of projects (none = all of them). Names are kept
-- as they were, so a sent report reads the same after a project is renamed or
-- deleted.
CREATE TABLE report_projects (
    report_id  UUID NOT NULL REFERENCES reports (id) ON DELETE CASCADE,
    position   INT NOT NULL,
    project_id UUID REFERENCES projects (id) ON DELETE SET NULL,
    name       TEXT NOT NULL,
    PRIMARY KEY (report_id, position)
);

INSERT INTO report_projects (report_id, position, project_id, name)
SELECT id, 0, project_id, project_name FROM reports WHERE project_name <> '';

ALTER TABLE reports DROP COLUMN project_id;
ALTER TABLE reports DROP COLUMN project_name;
