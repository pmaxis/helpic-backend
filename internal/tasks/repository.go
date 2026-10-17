package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/pmaxis/helpic/internal/user"
)

// Repository handles PostgreSQL access for tasks. Every query is scoped to
// tasks where the user is the freelancer or the client.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository creates a tasks Repository backed by the given pool.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

const selectTask = `SELECT t.id, t.created_by, t.title, t.description, t.status, t.status_id, ws.name, t.position,
		t.due_on, t.price_cents, f.currency, t.priority,
		coalesce((SELECT json_agg(json_build_object('id', l.id, 'name', l.name, 'color', l.color) ORDER BY lower(l.name))
			FROM task_labels tl JOIN labels l ON l.id = tl.label_id WHERE tl.task_id = t.id), '[]'),
		t.created_at, t.updated_at,
		f.id, f.name, f.email, c.id, c.name, c.email, pr.id, pr.name,
		coalesce(cr.name, ''), coalesce(cr.email, ''),
		-- who took part (kept by triggers in task_participants): the freelancer
		-- first, then in the order they first did something on the task
		coalesce((SELECT json_agg(json_build_object('id', u.id, 'name', u.name, 'email', u.email)
				ORDER BY tp.user_id = t.freelancer_id DESC, tp.first_at, u.id)
			FROM task_participants tp JOIN users u ON u.id = tp.user_id
			WHERE tp.task_id = t.id), '[]')
	FROM tasks t
	JOIN workflow_statuses ws ON ws.id = t.status_id
	JOIN users f ON f.id = t.freelancer_id
	LEFT JOIN users c ON c.id = t.client_id
	LEFT JOIN projects pr ON pr.id = t.project_id
	LEFT JOIN users cr ON cr.id = t.created_by`

func scanTask(row pgx.Row) (Task, error) {
	var (
		t                          Task
		clientID, clientName, mail *string
		projectID, projectName     *string
		labels                     []byte
		participants               []byte
	)
	err := row.Scan(&t.ID, &t.CreatedBy, &t.Title, &t.Description, &t.Status, &t.StatusID, &t.StatusName, &t.Position,
		&t.DueOn, &t.PriceCents, &t.Currency, &t.Priority, &labels, &t.CreatedAt, &t.UpdatedAt,
		&t.Freelancer.ID, &t.Freelancer.Name, &t.Freelancer.Email,
		&clientID, &clientName, &mail, &projectID, &projectName,
		&t.Creator.Name, &t.Creator.Email, &participants)
	if err != nil {
		return t, err
	}
	t.Creator.ID = t.CreatedBy
	if err := json.Unmarshal(participants, &t.Participants); err != nil {
		return t, fmt.Errorf("decode participants: %w", err)
	}
	if err := json.Unmarshal(labels, &t.Labels); err != nil {
		return t, fmt.Errorf("decode labels: %w", err)
	}
	if clientID != nil {
		t.Client = &Party{ID: *clientID, Name: *clientName, Email: *mail}
	}
	if projectID != nil {
		t.Project = &ProjectRef{ID: *projectID, Name: *projectName}
	}
	return t, nil
}

// ownerColumns returns the condition matching the user's ($1) side of a task
// and the column holding the other side. A client acts for their company, so
// a member sees the owner's tasks.
func ownerColumns(role string) (own, other string) {
	if role == user.RoleClient {
		return "t.client_id = acting_client($1)", "t.freelancer_id"
	}
	return "t.freelancer_id = $1", "t.client_id"
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

// List returns the user's tasks: open ones first, then by due date and newest.
func (r *Repository) List(ctx context.Context, userID, role string, f Filter) ([]Task, error) {
	own, other := ownerColumns(role)
	q := selectTask + ` WHERE ` + own + `
		AND (cardinality($2::text[]) = 0 OR t.status = ANY($2))
		AND ($3 = '' OR ` + other + `::text = $3
			OR ($3 = '` + PersonalFilter + `' AND ` + other + ` IS NULL))
		AND ($4 = '' OR t.project_id::text = $4)
		ORDER BY t.status IN ('done', 'cancelled'), t.due_on ASC NULLS LAST, t.created_at DESC`
	statuses := f.Statuses
	if statuses == nil {
		statuses = []string{}
	}
	rows, err := r.db.Query(ctx, q, userID, statuses, f.CounterpartID, f.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Task, error) { return scanTask(row) })
	if err != nil {
		return nil, fmt.Errorf("scan tasks: %w", err)
	}
	return out, nil
}

// Get loads a task the user takes part in, or ErrNotFound.
func (r *Repository) Get(ctx context.Context, userID, id string) (Task, error) {
	t, err := scanTask(r.db.QueryRow(ctx, selectTask+`
		WHERE t.id::text = $1 AND (t.freelancer_id = $2 OR t.client_id = acting_client($2))`, id, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	if err != nil {
		return Task{}, fmt.Errorf("get task: %w", err)
	}
	return t, nil
}

// ProjectPair returns the freelancer and client (nil for a personal project)
// of a project the user takes part in, or ErrProjectNotFound.
func (r *Repository) ProjectPair(ctx context.Context, userID, projectID string) (string, *string, error) {
	var (
		freelancerID string
		clientID     *string
	)
	err := r.db.QueryRow(ctx, `SELECT freelancer_id, client_id FROM projects
		WHERE id::text = $1 AND (freelancer_id = $2 OR client_id = acting_client($2))`, projectID, userID).
		Scan(&freelancerID, &clientID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, ErrProjectNotFound
	}
	if err != nil {
		return "", nil, fmt.Errorf("get project pair: %w", err)
	}
	return freelancerID, clientID, nil
}

// Status returns a workflow status's pair and category, or ErrStatusNotFound.
func (r *Repository) Status(ctx context.Context, statusID string) (freelancerID string, clientID *string, category string, err error) {
	err = r.db.QueryRow(ctx, `SELECT freelancer_id, client_id, category FROM workflow_statuses WHERE id::text = $1`, statusID).
		Scan(&freelancerID, &clientID, &category)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, "", ErrStatusNotFound
	}
	if err != nil {
		return "", nil, "", fmt.Errorf("get status: %w", err)
	}
	return freelancerID, clientID, category, nil
}

// pickStatus selects the new task's status: the given one ($s) or, when $s is
// empty, the pair's first "new" column — or its first column at all when the
// board has no "new" one. $f and $c are the pair, as uuid expressions.
const pickStatus = `SELECT id, category FROM workflow_statuses
	WHERE freelancer_id = $f AND client_id IS NOT DISTINCT FROM $c
	  AND ($s = '' OR id::text = $s)
	ORDER BY category = 'new' DESC, position LIMIT 1`

// CreatePersonal inserts a freelancer's personal task, which has no client.
func (r *Repository) CreatePersonal(ctx context.Context, freelancerID, projectID, statusID string, p Params) (string, error) {
	if _, err := r.db.Exec(ctx, `SELECT ensure_workflow($1, NULL)`, freelancerID); err != nil {
		return "", fmt.Errorf("ensure workflow: %w", err)
	}
	var id string
	err := r.db.QueryRow(ctx, `WITH st AS (`+bind(pickStatus, "$1::uuid", "NULL::uuid", "$6")+`)
		INSERT INTO tasks (freelancer_id, created_by, project_id, title, description, due_on, price_cents, priority, status, status_id, completed_at)
		SELECT $1::uuid, $1::uuid, NULLIF($2, '')::uuid, $3, $4, $5, $7, $8, st.category, st.id,
			CASE WHEN st.category = 'done' THEN now() END
		FROM st
		RETURNING id`, freelancerID, projectID, p.Title, p.Description, p.DueOn, statusID, p.PriceCents, p.Priority).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrStatusNotFound
	}
	if err != nil {
		return "", fmt.Errorf("create personal task: %w", err)
	}
	return id, nil
}

// Create inserts a task between a linked freelancer and client; when they
// aren't linked it returns ErrNotLinked.
func (r *Repository) Create(ctx context.Context, freelancerID, clientID, createdBy, projectID, statusID string, p Params) (string, error) {
	var linked bool
	err := r.db.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM freelancer_clients WHERE freelancer_id::text = $1 AND client_id::text = $2)`,
		freelancerID, clientID).Scan(&linked)
	if err != nil {
		return "", fmt.Errorf("check link: %w", err)
	}
	if !linked {
		return "", ErrNotLinked
	}
	if _, err := r.db.Exec(ctx, `SELECT ensure_workflow($1::uuid, $2::uuid)`, freelancerID, clientID); err != nil {
		return "", fmt.Errorf("ensure workflow: %w", err)
	}

	var id string
	err = r.db.QueryRow(ctx, `WITH st AS (`+bind(pickStatus, "$1::uuid", "$2::uuid", "$8")+`)
		INSERT INTO tasks (freelancer_id, client_id, created_by, project_id, title, description, due_on, price_cents, priority, status, status_id, completed_at)
		SELECT $1::uuid, $2::uuid, $3, NULLIF($4, '')::uuid, $5, $6, $7, $9, $10, st.category, st.id,
			CASE WHEN st.category = 'done' THEN now() END
		FROM st
		RETURNING id`, freelancerID, clientID, createdBy, projectID, p.Title, p.Description, p.DueOn, statusID, p.PriceCents, p.Priority).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrStatusNotFound
	}
	if err != nil {
		return "", fmt.Errorf("create task: %w", err)
	}
	return id, nil
}

// bind fills pickStatus's $f, $c and $s placeholders with query parameters.
func bind(q, f, c, s string) string {
	return strings.NewReplacer("$f", f, "$c", c, "$s", s).Replace(q)
}

// Update rewrites the task's content and project ("" = no project).
func (r *Repository) Update(ctx context.Context, id, projectID string, p Params) error {
	_, err := r.db.Exec(ctx, `UPDATE tasks
		SET title = $2, description = $3, due_on = $4, project_id = NULLIF($5, '')::uuid
		WHERE id = $1`, id, p.Title, p.Description, p.DueOn, projectID)
	if err != nil {
		return fmt.Errorf("update task: %w", err)
	}
	return nil
}

// SetPrice sets the task's price in cents (nil clears it).
func (r *Repository) SetPrice(ctx context.Context, id string, cents *int64) error {
	if _, err := r.db.Exec(ctx, `UPDATE tasks SET price_cents = $2 WHERE id = $1`, id, cents); err != nil {
		return fmt.Errorf("set task price: %w", err)
	}
	return nil
}

// SetPriority sets the task's priority.
func (r *Repository) SetPriority(ctx context.Context, id, priority string) error {
	if _, err := r.db.Exec(ctx, `UPDATE tasks SET priority = $2 WHERE id = $1`, id, priority); err != nil {
		return fmt.Errorf("set task priority: %w", err)
	}
	return nil
}

// SetLabels replaces the task's labels. Every label must be on the task's
// pair's board, or nothing changes and it returns ErrLabelMismatch.
func (r *Repository) SetLabels(ctx context.Context, id string, labelIDs []string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var matching int
	err = tx.QueryRow(ctx, `SELECT count(*) FROM labels l JOIN tasks t ON t.id = $1
		WHERE l.id::text = ANY($2) AND l.freelancer_id = t.freelancer_id AND l.client_id IS NOT DISTINCT FROM t.client_id`,
		id, labelIDs).Scan(&matching)
	if err != nil {
		return fmt.Errorf("check labels: %w", err)
	}
	if matching != len(labelIDs) {
		return ErrLabelMismatch
	}
	if _, err := tx.Exec(ctx, `DELETE FROM task_labels WHERE task_id = $1`, id); err != nil {
		return fmt.Errorf("clear labels: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO task_labels (task_id, label_id) SELECT $1, unnest($2::uuid[])`, id, labelIDs); err != nil {
		return fmt.Errorf("set labels: %w", err)
	}
	// The task changed, even though no column of its row did.
	if _, err := tx.Exec(ctx, `UPDATE tasks SET updated_at = now() WHERE id = $1`, id); err != nil {
		return fmt.Errorf("touch task: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// SetStatus moves the task to the first workflow status of a category,
// provided it is still in the category the caller checked; a concurrent change
// yields ErrTransition, and a board without a column of that category
// ErrStatusNotFound.
func (r *Repository) SetStatus(ctx context.Context, id, from, to string) error {
	var statusID *string
	err := r.db.QueryRow(ctx, `SELECT ws.id FROM tasks t
		JOIN workflow_statuses ws ON ws.freelancer_id = t.freelancer_id
		  AND ws.client_id IS NOT DISTINCT FROM t.client_id AND ws.category = $2
		WHERE t.id = $1
		ORDER BY ws.position LIMIT 1`, id, to).Scan(&statusID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrStatusNotFound
	}
	if err != nil {
		return fmt.Errorf("find status: %w", err)
	}
	tag, err := r.db.Exec(ctx, `UPDATE tasks SET status = $3, status_id = $4, position = NULL,
			completed_at = CASE WHEN $3 = 'done' THEN coalesce(completed_at, now()) END
		WHERE id = $1 AND status = $2`, id, from, to, *statusID)
	if err != nil {
		return fmt.Errorf("set task status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrTransition
	}
	return nil
}

// MoveTo puts the task into a workflow status (with its category), provided it
// is still in the status the caller checked; a concurrent change yields
// ErrTransition.
func (r *Repository) MoveTo(ctx context.Context, id, fromStatusID, toStatusID, category string) error {
	tag, err := r.db.Exec(ctx, `UPDATE tasks SET status_id = $3, status = $4,
			completed_at = CASE WHEN $4 = 'done' THEN coalesce(completed_at, now()) END
		WHERE id = $1 AND status_id = $2`,
		id, fromStatusID, toStatusID, category)
	if err != nil {
		return fmt.Errorf("move task: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrTransition
	}
	return nil
}

// Delete removes the task.
func (r *Repository) Delete(ctx context.Context, id string) error {
	if _, err := r.db.Exec(ctx, `DELETE FROM tasks WHERE id = $1`, id); err != nil {
		return fmt.Errorf("delete task: %w", err)
	}
	return nil
}
