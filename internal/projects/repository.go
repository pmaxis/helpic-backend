package projects

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/pmaxis/helpic/internal/user"
)

// Repository handles PostgreSQL access for projects. Every read is scoped to
// projects the user takes part in.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository creates a projects Repository backed by the given pool.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// seenBy keeps the participants u of the project p the viewer may see. The
// author, who invites them, and the client's side, who picks who does a task,
// see every freelancer; another freelancer sees none on it but the author.
func seenBy(viewer string) string {
	return `(u.role <> 'freelancer' OR u.id = ` + viewer + ` OR u.id = p.created_by OR p.created_by = ` + viewer + `
		OR (SELECT vu.role FROM users vu WHERE vu.id = ` + viewer + `) <> 'freelancer')`
}

// selectProject reads projects for the user in the viewer parameter: the task
// counts are of the tasks they see, so a contractor counts only theirs, and
// the participants are those they may see.
func selectProject(viewer string) string {
	return `SELECT p.id, p.created_by, p.name, p.description, p.created_at, p.updated_at,
		f.id, f.name, f.email, c.id, c.name, c.email,
		(SELECT count(*) FROM tasks t WHERE t.project_id = p.id AND t.status IN ('new', 'in_progress')
			AND sees_task(` + viewer + `, t.id, t.freelancer_id, t.client_id)),
		(SELECT count(*) FROM tasks t WHERE t.project_id = p.id
			AND sees_task(` + viewer + `, t.id, t.freelancer_id, t.client_id)),
		coalesce((SELECT json_agg(json_build_object(
				'id', u.id, 'name', u.name, 'email', u.email,
				'role', CASE
					WHEN u.role = 'freelancer' THEN 'freelancer'
					WHEN u.id = p.client_id THEN 'owner'
					ELSE coalesce((SELECT m.role FROM company_members m
						JOIN companies co ON co.id = m.company_id
						WHERE m.user_id = u.id AND co.owner_id = p.client_id), 'member')
				END)
				ORDER BY u.id = p.created_by DESC, lower(u.name), u.email)
			FROM project_participants pp JOIN users u ON u.id = pp.user_id
			WHERE pp.project_id = p.id AND ` + seenBy(viewer) + `), '[]'),
		(SELECT pp.hourly_rate_cents FROM project_participants pp WHERE pp.project_id = p.id AND pp.user_id = ` + viewer + `)
	FROM projects p
	JOIN users f ON f.id = p.freelancer_id
	LEFT JOIN users c ON c.id = p.client_id`
}

func scanProject(row pgx.Row) (Project, error) {
	var (
		p                          Project
		clientID, clientName, mail *string
		participants               []byte
	)
	err := row.Scan(&p.ID, &p.CreatedBy, &p.Name, &p.Description, &p.CreatedAt, &p.UpdatedAt,
		&p.Freelancer.ID, &p.Freelancer.Name, &p.Freelancer.Email,
		&clientID, &clientName, &mail, &p.OpenTasks, &p.TotalTasks, &participants, &p.MyRateCents)
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal(participants, &p.Participants); err != nil {
		return p, fmt.Errorf("decode participants: %w", err)
	}
	if clientID != nil {
		p.Client = &Party{ID: *clientID, Name: *clientName, Email: *mail}
	}
	return p, nil
}

// List returns the user's projects, alphabetically. counterpartID keeps the
// projects with one client (for a freelancer) or one freelancer (for a
// client); PersonalFilter keeps a freelancer's personal projects. A project
// shows up for its participants (sees_project).
func (r *Repository) List(ctx context.Context, userID, role, counterpartID string) ([]Project, error) {
	other := "p.client_id"
	if role == user.RoleClient {
		other = "p.freelancer_id"
	}
	rows, err := r.db.Query(ctx, selectProject("$1::uuid")+` WHERE sees_project($1::uuid, p.id)
		AND ($2 = '' OR `+other+`::text = $2 OR ($2 = '`+PersonalFilter+`' AND `+other+` IS NULL))
		ORDER BY lower(p.name), p.created_at`, userID, counterpartID)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Project, error) { return scanProject(row) })
	if err != nil {
		return nil, fmt.Errorf("scan projects: %w", err)
	}
	return out, nil
}

// Get loads a project the user takes part in, or ErrNotFound.
func (r *Repository) Get(ctx context.Context, userID, id string) (Project, error) {
	p, err := scanProject(r.db.QueryRow(ctx, selectProject("$2::uuid")+`
		WHERE p.id::text = $1 AND sees_project($2::uuid, p.id)`, id, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	if err != nil {
		return Project{}, fmt.Errorf("get project: %w", err)
	}
	return p, nil
}

// ActingClient is the client the user acts as: their company's owner for a
// member, themselves otherwise.
func (r *Repository) ActingClient(ctx context.Context, userID string) (string, error) {
	var id string
	if err := r.db.QueryRow(ctx, `SELECT acting_client($1::uuid)::text`, userID).Scan(&id); err != nil {
		return "", fmt.Errorf("acting client: %w", err)
	}
	return id, nil
}

// IsContractor reports whether the user is a client company's contractor.
func (r *Repository) IsContractor(ctx context.Context, userID string) (bool, error) {
	var ok bool
	if err := r.db.QueryRow(ctx, `SELECT is_contractor($1)`, userID).Scan(&ok); err != nil {
		return false, fmt.Errorf("check contractor: %w", err)
	}
	return ok, nil
}

// CreatePersonal inserts a freelancer's personal project. They are its only participant.
func (r *Repository) CreatePersonal(ctx context.Context, freelancerID string, p Params) (string, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var id string
	err = tx.QueryRow(ctx, `INSERT INTO projects (freelancer_id, created_by, name, description)
		VALUES ($1, $1, $2, $3) RETURNING id`, freelancerID, p.Name, p.Description).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("create personal project: %w", err)
	}
	if err := addParticipants(ctx, tx, id, freelancerID, freelancerID); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `SELECT ensure_project_workflow($1)`, id); err != nil {
		return "", fmt.Errorf("ensure workflow: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit project: %w", err)
	}
	return id, nil
}

// Create inserts a project between a client and the freelancers they chose.
// freelancerIDs[0] is stored on the project; every one of them, and the
// author, start as participants. Anyone who isn't linked yields ErrNotLinked.
func (r *Repository) Create(ctx context.Context, freelancerIDs []string, clientID, createdBy string, p Params) (string, error) {
	if len(freelancerIDs) == 0 {
		return "", ErrNotLinked
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var linked int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM freelancer_clients
		WHERE client_id::text = $1 AND freelancer_id::text = ANY($2::text[])`, clientID, freelancerIDs).Scan(&linked); err != nil {
		return "", fmt.Errorf("check links: %w", err)
	}
	if linked != len(freelancerIDs) {
		return "", ErrNotLinked
	}

	var id string
	err = tx.QueryRow(ctx, `INSERT INTO projects (freelancer_id, client_id, created_by, name, description)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id`, freelancerIDs[0], clientID, createdBy, p.Name, p.Description).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("create project: %w", err)
	}
	if err := addParticipants(ctx, tx, id, createdBy, append([]string{createdBy}, freelancerIDs...)...); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `SELECT ensure_project_workflow($1)`, id); err != nil {
		return "", fmt.Errorf("ensure workflow: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit project: %w", err)
	}
	return id, nil
}

// whoCanJoin is every freelancer the client works with, the client, and the
// client's employees and contractors. A personal project has only its freelancer.
const whoCanJoin = `
	SELECT fc.freelancer_id AS id, 'freelancer' AS role
	FROM projects p
	JOIN freelancer_clients fc ON fc.client_id = p.client_id
	WHERE p.id = $1::uuid
	UNION
	SELECT p.freelancer_id, 'freelancer' FROM projects p WHERE p.id = $1::uuid AND p.client_id IS NULL
	UNION
	SELECT p.client_id, 'owner' FROM projects p WHERE p.id = $1::uuid AND p.client_id IS NOT NULL
	UNION
	SELECT m.user_id, m.role
	FROM projects p
	JOIN companies c ON c.owner_id = p.client_id
	JOIN company_members m ON m.company_id = c.id
	WHERE p.id = $1::uuid AND m.role IN ('member', 'contractor')`

// Participants lists who is on the project as the user sees them: the author
// first, then by name.
func (r *Repository) Participants(ctx context.Context, userID, projectID string) ([]Person, error) {
	rows, err := r.db.Query(ctx, `SELECT u.id, u.name, u.email, j.role
		FROM project_participants pp
		JOIN projects p ON p.id = pp.project_id
		JOIN users u ON u.id = pp.user_id
		JOIN (`+whoCanJoin+`) j ON j.id = u.id
		WHERE pp.project_id = $1::uuid AND `+seenBy("$2::uuid")+`
		ORDER BY u.id = p.created_by DESC, lower(u.name), u.email`, projectID, userID)
	if err != nil {
		return nil, fmt.Errorf("list project participants: %w", err)
	}
	return collectPeople(rows)
}

// Candidates lists who the author may still invite.
func (r *Repository) Candidates(ctx context.Context, projectID string) ([]Person, error) {
	rows, err := r.db.Query(ctx, `SELECT u.id, u.name, u.email, j.role
		FROM (`+whoCanJoin+`) j
		JOIN users u ON u.id = j.id
		WHERE NOT EXISTS (
			SELECT 1 FROM project_participants pp WHERE pp.project_id = $1::uuid AND pp.user_id = u.id
		)
		ORDER BY j.role, lower(u.name), u.email`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list invite candidates: %w", err)
	}
	return collectPeople(rows)
}

func collectPeople(rows pgx.Rows) ([]Person, error) {
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Person, error) {
		var p Person
		return p, row.Scan(&p.ID, &p.Name, &p.Email, &p.Role)
	})
	if err != nil {
		return nil, fmt.Errorf("scan people: %w", err)
	}
	return out, nil
}

// Invite puts someone on the project. False means they aren't someone the
// author can invite; true with already means they were on it.
func (r *Repository) Invite(ctx context.Context, projectID, userID, invitedBy string) (added bool, err error) {
	var id string
	err = r.db.QueryRow(ctx, `INSERT INTO project_participants (project_id, user_id, invited_by)
		SELECT $1::uuid, j.id, $3::uuid FROM (`+whoCanJoin+`) j WHERE j.id::text = $2
		ON CONFLICT DO NOTHING
		RETURNING user_id`, projectID, userID, invitedBy).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		var on bool
		if err := r.db.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1 FROM project_participants WHERE project_id = $1::uuid AND user_id::text = $2)`, projectID, userID).Scan(&on); err != nil {
			return false, fmt.Errorf("check participant: %w", err)
		}
		if on {
			return false, nil
		}
		return false, ErrNotInvitable
	}
	if err != nil {
		return false, fmt.Errorf("invite to project: %w", err)
	}
	return true, nil
}

// RemoveParticipant takes someone off the project. False means they weren't on it.
func (r *Repository) RemoveParticipant(ctx context.Context, projectID, userID string) (bool, error) {
	tag, err := r.db.Exec(ctx, `DELETE FROM project_participants WHERE project_id = $1::uuid AND user_id::text = $2`, projectID, userID)
	if err != nil {
		return false, fmt.Errorf("remove project participant: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// addParticipants inserts the given users, skipping blanks and repeats.
func addParticipants(ctx context.Context, tx pgx.Tx, projectID, invitedBy string, userIDs ...string) error {
	seen := map[string]bool{}
	var ids []string
	for _, id := range userIDs {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO project_participants (project_id, user_id, invited_by)
		SELECT $1::uuid, u::uuid, $2::uuid FROM unnest($3::text[]) AS u
		ON CONFLICT DO NOTHING`, projectID, invitedBy, ids); err != nil {
		return fmt.Errorf("add project participants: %w", err)
	}
	return nil
}

// SetRate sets or clears a participant's hourly rate on the project.
func (r *Repository) SetRate(ctx context.Context, projectID, userID string, cents *int64) error {
	if _, err := r.db.Exec(ctx, `UPDATE project_participants SET hourly_rate_cents = $3 WHERE project_id = $1 AND user_id = $2`,
		projectID, userID, cents); err != nil {
		return fmt.Errorf("set project rate: %w", err)
	}
	return nil
}

// Update rewrites the project's name and description.
func (r *Repository) Update(ctx context.Context, id string, p Params) error {
	if _, err := r.db.Exec(ctx, `UPDATE projects SET name = $2, description = $3 WHERE id = $1`,
		id, p.Name, p.Description); err != nil {
		return fmt.Errorf("update project: %w", err)
	}
	return nil
}

// Delete removes the project. Its tasks stay, on the pair's workflow, since the
// project's columns go with it.
func (r *Repository) Delete(ctx context.Context, id string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM projects WHERE id = $1)`, id).Scan(&exists); err != nil {
		return fmt.Errorf("get project: %w", err)
	}
	if !exists {
		return nil
	}
	var unassigned bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM tasks WHERE project_id = $1 AND freelancer_id IS NULL)`, id).Scan(&unassigned); err != nil {
		return fmt.Errorf("check unassigned tasks: %w", err)
	}
	if unassigned {
		return ErrUnassignedTasks
	}
	if _, err := tx.Exec(ctx, `SELECT ensure_workflow(freelancer_id, client_id)
		FROM (SELECT DISTINCT freelancer_id, client_id FROM tasks WHERE project_id = $1) pairs`, id); err != nil {
		return fmt.Errorf("ensure workflow: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE tasks t
		SET status_id = picked.id, status = picked.category
		FROM (
			SELECT t2.id AS task_id, ws.id, ws.category
			FROM tasks t2
			JOIN workflow_statuses cur ON cur.id = t2.status_id
			JOIN LATERAL (
				SELECT w2.id, w2.category
				FROM workflow_statuses w2
				WHERE w2.freelancer_id = t2.freelancer_id
				  AND w2.client_id IS NOT DISTINCT FROM t2.client_id
				  AND w2.project_id IS NULL
				ORDER BY (w2.category = cur.category) DESC, (lower(w2.name) = lower(cur.name)) DESC, w2.position
				LIMIT 1
			) ws ON true
			WHERE t2.project_id = $1
		) picked
		WHERE t.id = picked.task_id`, id); err != nil {
		return fmt.Errorf("move tasks off the project: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM projects WHERE id = $1`, id); err != nil {
		return fmt.Errorf("delete project: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit project delete: %w", err)
	}
	return nil
}
