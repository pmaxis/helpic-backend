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
// the tasks the user sees (the sees_task SQL function): their freelancer's,
// their client company's — for a contractor, only the ones they take part in.
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
// a member sees the owner's tasks, and a contractor the ones they take part in.
func ownerColumns(role string) (own, other string) {
	if role == user.RoleClient {
		return "t.client_id = acting_client($1) AND sees_task($1, t.id, t.freelancer_id, t.client_id)", "t.freelancer_id"
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

// IsContractor reports whether the user is a client company's contractor.
func (r *Repository) IsContractor(ctx context.Context, userID string) (bool, error) {
	var ok bool
	if err := r.db.QueryRow(ctx, `SELECT is_contractor($1)`, userID).Scan(&ok); err != nil {
		return false, fmt.Errorf("check contractor: %w", err)
	}
	return ok, nil
}

// ClientPeople lists the task's client side: the client first, then their
// company's people by name, saying who is on the task already.
func (r *Repository) ClientPeople(ctx context.Context, taskID string) ([]Person, error) {
	rows, err := r.db.Query(ctx, `SELECT u.id, u.name, u.email, coalesce(m.role, 'owner'),
			EXISTS (SELECT 1 FROM task_participants tp WHERE tp.task_id = t.id AND tp.user_id = u.id)
		FROM tasks t
		JOIN users u ON u.id = t.client_id
			OR u.id IN (SELECT cm.user_id FROM company_members cm JOIN companies c ON c.id = cm.company_id WHERE c.owner_id = t.client_id)
		LEFT JOIN company_members m ON m.user_id = u.id
		WHERE t.id = $1
		ORDER BY u.id = t.client_id DESC, lower(u.name), u.email`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list client people: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Person, error) {
		var p Person
		return p, row.Scan(&p.ID, &p.Name, &p.Email, &p.Role, &p.OnTask)
	})
	if err != nil {
		return nil, fmt.Errorf("scan client people: %w", err)
	}
	return out, nil
}

// ClientSideParticipants returns who on the task's client side takes part in
// it, other than the client: their company's people, while they're still in
// it.
func (r *Repository) ClientSideParticipants(ctx context.Context, taskID, clientID string) ([]string, error) {
	rows, err := r.db.Query(ctx, `SELECT tp.user_id::text FROM task_participants tp
		WHERE tp.task_id = $1 AND tp.user_id <> $2 AND acting_client(tp.user_id) = $2
		ORDER BY tp.first_at`, taskID, clientID)
	if err != nil {
		return nil, fmt.Errorf("list client side participants: %w", err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("scan client side participants: %w", err)
	}
	return out, nil
}

// AddParticipant puts someone on the task.
func (r *Repository) AddParticipant(ctx context.Context, taskID, userID string) error {
	if _, err := r.db.Exec(ctx, `SELECT note_participant($1, $2, now())`, taskID, userID); err != nil {
		return fmt.Errorf("add participant: %w", err)
	}
	return nil
}

// RemoveParticipant takes someone off the task.
func (r *Repository) RemoveParticipant(ctx context.Context, taskID, userID string) error {
	if _, err := r.db.Exec(ctx, `DELETE FROM task_participants WHERE task_id = $1 AND user_id::text = $2`, taskID, userID); err != nil {
		return fmt.Errorf("remove participant: %w", err)
	}
	return nil
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

// Get loads a task the user sees, or ErrNotFound.
func (r *Repository) Get(ctx context.Context, userID, id string) (Task, error) {
	t, err := scanTask(r.db.QueryRow(ctx, selectTask+`
		WHERE t.id::text = $1 AND sees_task($2, t.id, t.freelancer_id, t.client_id)`, id, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	if err != nil {
		return Task{}, fmt.Errorf("get task: %w", err)
	}
	return t, nil
}

// ProjectPair returns the freelancer and client (nil for a personal project)
// of a project the user may put a task in, or ErrProjectNotFound. A company
// person may file a task into the company's project before it is visible to
// them; seeing the project is sees_project.
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

// IsProjectAuthor reports whether the user created the project.
func (r *Repository) IsProjectAuthor(ctx context.Context, userID, projectID string) (bool, error) {
	if projectID == "" {
		return false, nil
	}
	var ok bool
	err := r.db.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM projects WHERE id::text = $1 AND created_by = $2::uuid)`, projectID, userID).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("project author: %w", err)
	}
	return ok, nil
}

// WorkflowStatus is a board column's category, as a move needs it.
type WorkflowStatus struct {
	FreelancerID        string
	ClientID            *string
	ProjectID           string
	Bucket              string
	CategoryID          string
	ParticipantsCanMove bool
}

// Status returns a workflow status's pair and category, or ErrStatusNotFound.
func (r *Repository) Status(ctx context.Context, statusID string) (WorkflowStatus, error) {
	var s WorkflowStatus
	err := r.db.QueryRow(ctx, `SELECT ws.freelancer_id, ws.client_id, coalesce(ws.project_id::text, ''), ws.category, ws.category_id::text, c.participants_can_move
		FROM workflow_statuses ws
		JOIN workflow_categories c ON c.id = ws.category_id
		WHERE ws.id::text = $1`, statusID).
		Scan(&s.FreelancerID, &s.ClientID, &s.ProjectID, &s.Bucket, &s.CategoryID, &s.ParticipantsCanMove)
	if errors.Is(err, pgx.ErrNoRows) {
		return WorkflowStatus{}, ErrStatusNotFound
	}
	if err != nil {
		return WorkflowStatus{}, fmt.Errorf("get status: %w", err)
	}
	return s, nil
}

// pickStatus selects the new task's status: the given one ($s) or, when $s is
// empty, the workflow's first "new" column — or its first column at all when
// the board has no "new" one. $f, $c and $p are the workflow, as uuid expressions.
const pickStatus = `SELECT id, category FROM workflow_statuses
	WHERE freelancer_id = $f AND client_id IS NOT DISTINCT FROM $c
	  AND project_id IS NOT DISTINCT FROM $p
	  AND ($s = '' OR id::text = $s)
	ORDER BY category = 'new' DESC, position LIMIT 1`

// ensureTaskWorkflow creates the defaults of the workflow a new task lands
// on: the project's, or the pair's when the task is outside any project.
func (r *Repository) ensureTaskWorkflow(ctx context.Context, freelancerID, clientID, projectID string) error {
	var err error
	if projectID != "" {
		_, err = r.db.Exec(ctx, `SELECT ensure_project_workflow($1::uuid)`, projectID)
	} else if clientID == "" {
		_, err = r.db.Exec(ctx, `SELECT ensure_workflow($1::uuid, NULL)`, freelancerID)
	} else {
		_, err = r.db.Exec(ctx, `SELECT ensure_workflow($1::uuid, $2::uuid)`, freelancerID, clientID)
	}
	if err != nil {
		return fmt.Errorf("ensure workflow: %w", err)
	}
	return nil
}

// CreatePersonal inserts a freelancer's personal task, which has no client.
func (r *Repository) CreatePersonal(ctx context.Context, freelancerID, projectID, statusID string, p Params) (string, error) {
	if err := r.ensureTaskWorkflow(ctx, freelancerID, "", projectID); err != nil {
		return "", err
	}
	var id string
	err := r.db.QueryRow(ctx, `WITH st AS (`+bind(pickStatus, "$1::uuid", "NULL::uuid", "NULLIF($2, '')::uuid", "$6")+`)
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
	if err := r.ensureTaskWorkflow(ctx, freelancerID, clientID, projectID); err != nil {
		return "", err
	}

	var id string
	err = r.db.QueryRow(ctx, `WITH st AS (`+bind(pickStatus, "$1::uuid", "$2::uuid", "NULLIF($4, '')::uuid", "$8")+`)
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

// bind fills pickStatus's $f, $c, $p and $s placeholders with query parameters.
func bind(q, f, c, p, s string) string {
	return strings.NewReplacer("$f", f, "$c", c, "$p", p, "$s", s).Replace(q)
}

// Update rewrites the task's content and project ("" = no project). Moving it
// to another project puts it in that project's column of the same category.
func (r *Repository) Update(ctx context.Context, id, projectID string, p Params) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var freelancerID string
	var clientID *string
	err = tx.QueryRow(ctx, `UPDATE tasks
		SET title = $2, description = $3, due_on = $4, project_id = NULLIF($5, '')::uuid
		WHERE id = $1
		RETURNING freelancer_id, client_id`, id, p.Title, p.Description, p.DueOn, projectID).Scan(&freelancerID, &clientID)
	if err != nil {
		return fmt.Errorf("update task: %w", err)
	}
	if projectID != "" {
		if _, err := tx.Exec(ctx, `SELECT ensure_project_workflow($1::uuid)`, projectID); err != nil {
			return fmt.Errorf("ensure workflow: %w", err)
		}
	} else if _, err := tx.Exec(ctx, `SELECT ensure_workflow($1, $2)`, freelancerID, clientID); err != nil {
		return fmt.Errorf("ensure workflow: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE tasks t SET status_id = picked.id, status = picked.category
		FROM (
			SELECT ws.id, ws.category
			FROM tasks t2
			JOIN workflow_statuses cur ON cur.id = t2.status_id
			JOIN workflow_statuses ws ON ws.freelancer_id = t2.freelancer_id
			  AND ws.client_id IS NOT DISTINCT FROM t2.client_id
			  AND ws.project_id IS NOT DISTINCT FROM t2.project_id
			WHERE t2.id = $1
			ORDER BY (ws.category = cur.category) DESC, (lower(ws.name) = lower(cur.name)) DESC, ws.position
			LIMIT 1
		) picked
		WHERE t.id = $1 AND t.status_id <> picked.id`, id); err != nil {
		return fmt.Errorf("retarget status: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit task: %w", err)
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
		  AND ws.client_id IS NOT DISTINCT FROM t.client_id
		  AND ws.project_id IS NOT DISTINCT FROM t.project_id
		  AND ws.category = $2
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
