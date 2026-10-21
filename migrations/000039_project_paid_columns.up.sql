-- A payment on a project with several freelancers made its Paid column with
-- the paying report's freelancer, not the project's, so the project's board
-- didn't show it and the paid tasks dropped off the board. A project's
-- columns all belong to the project's pair.
UPDATE workflow_statuses ws
SET freelancer_id = p.freelancer_id, client_id = p.client_id
FROM projects p
WHERE ws.project_id = p.id
  AND (ws.freelancer_id <> p.freelancer_id OR ws.client_id IS DISTINCT FROM p.client_id);
