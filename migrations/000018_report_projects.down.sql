ALTER TABLE reports ADD COLUMN project_id UUID REFERENCES projects (id) ON DELETE SET NULL;
ALTER TABLE reports ADD COLUMN project_name TEXT NOT NULL DEFAULT '';
UPDATE reports r SET project_id = rp.project_id, project_name = rp.name
FROM report_projects rp WHERE rp.report_id = r.id AND rp.position = 0;
DROP TABLE report_projects;
