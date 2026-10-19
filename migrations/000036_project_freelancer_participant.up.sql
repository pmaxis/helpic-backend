-- A freelancer chosen for a project is one of its participants. Projects
-- created before that rule get their freelancer added.
INSERT INTO project_participants (project_id, user_id, invited_by)
SELECT id, freelancer_id, created_by FROM projects
ON CONFLICT DO NOTHING;
