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
		t.due_on, t.price_cents, coalesce(f.currency, 'USD'), t.priority,
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
			WHERE tp.task_id = t.id), '[]'),
		coalesce(f.role, ''), t.archived_at, ar.id, ar.title,
		t.billing, task_seconds(t.id), task_billed(t.id),
		coalesce((SELECT pp.hourly_rate_cents FROM project_participants pp WHERE pp.project_id = t.project_id AND pp.user_id = t.freelancer_id),
			f.hourly_rate_cents)
	FROM tasks t
	JOIN workflow_statuses ws ON ws.id = t.status_id
	LEFT JOIN users f ON f.id = t.freelancer_id
	LEFT JOIN users c ON c.id = t.client_id
	LEFT JOIN projects pr ON pr.id = t.project_id
	LEFT JOIN users cr ON cr.id = t.created_by
	LEFT JOIN reports ar ON ar.id = t.archived_report_id`

func scanTask(row pgx.Row) (Task, error) {
	var (
		t                                             Task
		freelancerID, freelancerName, freelancerEmail *string
		clientID, clientName, mail                    *string
		projectID, projectName                        *string
		labels                                        []byte
		participants                                  []byte
		reportID, reportTitle                         *string
	)
	err := row.Scan(&t.ID, &t.CreatedBy, &t.Title, &t.Description, &t.Status, &t.StatusID, &t.StatusName, &t.Position,
		&t.DueOn, &t.PriceCents, &t.Currency, &t.Priority, &labels, &t.CreatedAt, &t.UpdatedAt,
		&freelancerID, &freelancerName, &freelancerEmail,
		&clientID, &clientName, &mail, &projectID, &projectName,
		&t.Creator.Name, &t.Creator.Email, &participants,
		&t.FreelancerRole, &t.ArchivedAt, &reportID, &reportTitle,
		&t.Billing, &t.TrackedSeconds, &t.Billed, &t.HourlyRateCents)
	if err != nil {
		return t, err
	}
	t.Creator.ID = t.CreatedBy
	if freelancerID != nil {
		name, email := "", ""
		if freelancerName != nil {
			name = *freelancerName
		}
		if freelancerEmail != nil {
			email = *freelancerEmail
		}
		t.Freelancer = Party{ID: *freelancerID, Name: name, Email: email}
	}
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
	if reportID != nil {
		t.ArchivedBy = &ReportRef{ID: *reportID, Title: *reportTitle}
	}
	return t, nil
}

// ownerColumns returns the condition matching the user's ($1) side of a task
// and the column holding the other side. A client acts for their company, so
// a member sees the owner's tasks, and a contractor the ones they take part
// in; their own personal tasks are theirs alone. A personal task's owner is
// in freelancer_id, whatever their role.
func ownerColumns(role string) (own, other string) {
	if role == user.RoleClient {
		return `((t.client_id = acting_client($1) AND sees_task($1, t.id, t.freelancer_id, t.client_id))
			OR (t.client_id IS NULL AND t.freelancer_id = $1))`, "t.freelancer_id"
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

// ClientPeople lists who can be on the task: freelancers who can take it, the
// client, then the client's company, and who is on it already.
func (r *Repository) ClientPeople(ctx context.Context, taskID string) ([]Person, error) {
	rows, err := r.db.Query(ctx, `SELECT u.id, u.name, u.email, j.role,
			EXISTS (SELECT 1 FROM task_participants tp WHERE tp.task_id = t.id AND tp.user_id = u.id)
				OR u.id IS NOT DISTINCT FROM t.freelancer_id
		FROM tasks t
		JOIN (
			SELECT t2.freelancer_id AS id, 'freelancer' AS role
			FROM tasks t2 WHERE t2.id = $1 AND t2.freelancer_id IS NOT NULL
			UNION
			SELECT pp.user_id, 'freelancer'
			FROM tasks t2
			JOIN project_participants pp ON pp.project_id = t2.project_id
			JOIN users fu ON fu.id = pp.user_id AND fu.role = 'freelancer'
			WHERE t2.id = $1
			UNION
			SELECT fc.freelancer_id, 'freelancer'
			FROM tasks t2
			JOIN freelancer_clients fc ON fc.client_id = t2.client_id
			WHERE t2.id = $1 AND t2.project_id IS NULL
			UNION
			SELECT t2.client_id, 'owner'
			FROM tasks t2 WHERE t2.id = $1 AND t2.client_id IS NOT NULL
			UNION
			SELECT cm.user_id, cm.role
			FROM tasks t2
			JOIN companies c ON c.owner_id = t2.client_id
			JOIN company_members cm ON cm.company_id = c.id
			WHERE t2.id = $1
		) j ON true
		JOIN users u ON u.id = j.id
		WHERE t.id = $1
		ORDER BY j.role = 'freelancer' DESC, j.role = 'owner' DESC, lower(u.name), u.email`, taskID)
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

// AssignFreelancer sets who the task is for. An empty freelancerID takes the
// assignee off; a personal task is never passed here. The previous assignee
// leaves the participants unless they created the task.
func (r *Repository) AssignFreelancer(ctx context.Context, taskID, freelancerID string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var old, createdBy, clientID, projectID string
	err = tx.QueryRow(ctx, `SELECT coalesce(freelancer_id::text, ''), created_by::text,
			coalesce(client_id::text, ''), coalesce(project_id::text, '')
		FROM tasks WHERE id = $1`, taskID).Scan(&old, &createdBy, &clientID, &projectID)
	if err != nil {
		return fmt.Errorf("read task assignee: %w", err)
	}
	// The price was the previous freelancer's, in their currency.
	if _, err := tx.Exec(ctx, `UPDATE tasks SET freelancer_id = NULLIF($2, '')::uuid, price_cents = NULL WHERE id = $1`, taskID, freelancerID); err != nil {
		return fmt.Errorf("assign freelancer: %w", err)
	}
	if freelancerID != "" {
		if _, err := tx.Exec(ctx, `SELECT note_participant($1, $2::uuid, now())`, taskID, freelancerID); err != nil {
			return fmt.Errorf("note freelancer: %w", err)
		}
		if projectID == "" {
			if _, err := tx.Exec(ctx, `SELECT ensure_workflow($1, NULLIF($2, '')::uuid)`, freelancerID, clientID); err != nil {
				return fmt.Errorf("ensure workflow: %w", err)
			}
			if _, err := tx.Exec(ctx, `UPDATE tasks t SET status_id = picked.id, status = picked.category
				FROM (
					SELECT ws.id, ws.category
					FROM tasks t2
					JOIN workflow_statuses cur ON cur.id = t2.status_id
					JOIN workflow_statuses ws ON ws.project_id IS NULL
						AND ws.freelancer_id = t2.freelancer_id
						AND ws.client_id IS NOT DISTINCT FROM t2.client_id
					WHERE t2.id = $1
					ORDER BY (ws.category = cur.category) DESC, (lower(ws.name) = lower(cur.name)) DESC, ws.position
					LIMIT 1
				) picked
				WHERE t.id = $1 AND t.status_id IS DISTINCT FROM picked.id`, taskID); err != nil {
				return fmt.Errorf("retarget status: %w", err)
			}
		}
	}
	if old != "" && old != freelancerID && old != createdBy {
		if _, err := tx.Exec(ctx, `DELETE FROM task_participants WHERE task_id = $1 AND user_id::text = $2`, taskID, old); err != nil {
			return fmt.Errorf("remove previous freelancer: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit assignee: %w", err)
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

// List returns the user's tasks on the boards, archived ones left out: open
// ones first, then by due date and newest.
func (r *Repository) List(ctx context.Context, userID, role string, f Filter) ([]Task, error) {
	own, other := ownerColumns(role)
	q := selectTask + ` WHERE ` + own + ` AND t.archived_at IS NULL
		AND (cardinality($2::text[]) = 0 OR t.status = ANY($2))
		AND ($3 = '' OR (t.client_id IS NOT NULL AND ` + other + `::text = $3)
			OR ($3 = '` + PersonalFilter + `' AND t.client_id IS NULL))
		AND ($4 = '' OR t.project_id::text = $4)
		ORDER BY t.status = 'done', t.due_on ASC NULLS LAST, t.created_at DESC`
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

// ProjectClient returns the client (nil for a personal project) of a project
// the user may put a task in, or ErrProjectNotFound. A participant may, and so
// may someone who acts for the project's client before they are invited.
func (r *Repository) ProjectClient(ctx context.Context, userID, projectID string) (*string, error) {
	var clientID *string
	err := r.db.QueryRow(ctx, `SELECT client_id FROM projects
		WHERE id::text = $1 AND (sees_project($2::uuid, id) OR client_id = acting_client($2::uuid))`,
		projectID, userID).Scan(&clientID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrProjectNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get project: %w", err)
	}
	return clientID, nil
}

// FreelancerParticipants lists the freelancers on a project, earliest first.
func (r *Repository) FreelancerParticipants(ctx context.Context, projectID string) ([]string, error) {
	rows, err := r.db.Query(ctx, `SELECT pp.user_id::text
		FROM project_participants pp
		JOIN users u ON u.id = pp.user_id AND u.role = 'freelancer'
		WHERE pp.project_id = $1::uuid
		ORDER BY pp.created_at, pp.user_id`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list project freelancers: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("scan project freelancers: %w", err)
	}
	return ids, nil
}

// Linked reports whether the freelancer works with the client.
func (r *Repository) Linked(ctx context.Context, freelancerID, clientID string) (bool, error) {
	var ok bool
	err := r.db.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM freelancer_clients WHERE freelancer_id::text = $1 AND client_id::text = $2)`,
		freelancerID, clientID).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("check link: %w", err)
	}
	return ok, nil
}

// IsFreelancerParticipant reports whether the user is a freelancer on the project.
func (r *Repository) IsFreelancerParticipant(ctx context.Context, projectID, userID string) (bool, error) {
	var ok bool
	err := r.db.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM project_participants pp
		JOIN users u ON u.id = pp.user_id AND u.role = 'freelancer'
		WHERE pp.project_id = $1::uuid AND pp.user_id::text = $2)`, projectID, userID).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("check project freelancer: %w", err)
	}
	return ok, nil
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

// WorkflowStatus is a board column, as a move needs it.
type WorkflowStatus struct {
	FreelancerID        string
	ClientID            *string
	ProjectID           string
	Bucket              string
	ParticipantsCanMove bool
}

// Status returns a workflow status's pair and bucket, or ErrStatusNotFound.
func (r *Repository) Status(ctx context.Context, statusID string) (WorkflowStatus, error) {
	var s WorkflowStatus
	err := r.db.QueryRow(ctx, `SELECT freelancer_id, client_id, coalesce(project_id::text, ''), category
		FROM workflow_statuses WHERE id::text = $1`, statusID).
		Scan(&s.FreelancerID, &s.ClientID, &s.ProjectID, &s.Bucket)
	s.ParticipantsCanMove = s.Bucket != StatusDone
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
// A task in a project uses that project's columns, whichever freelancer it is
// for. A task outside a project uses its pair's columns.
const pickStatus = `SELECT id, category FROM workflow_statuses
	WHERE (
	    ($p IS NOT NULL AND project_id = $p)
	    OR ($p IS NULL AND project_id IS NULL AND freelancer_id = $f AND client_id IS NOT DISTINCT FROM $c)
	  )
	  AND ($s = '' OR id::text = $s)
	ORDER BY category = 'new' DESC, position LIMIT 1`

// taskWorkflow matches a task's column: the project's, or the pair's when the
// task is outside any project. t is the tasks alias.
const taskWorkflow = `(
	(t.project_id IS NOT NULL AND ws.project_id = t.project_id)
	OR (t.project_id IS NULL AND ws.project_id IS NULL
		AND ws.freelancer_id = t.freelancer_id AND ws.client_id IS NOT DISTINCT FROM t.client_id)
)`

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

	// The freelancer is NULL while a project task waits for one.
	var freelancerID, clientID *string
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
	} else if freelancerID == nil {
		return ErrNeedsFreelancer
	} else if _, err := tx.Exec(ctx, `SELECT ensure_workflow($1, $2)`, *freelancerID, clientID); err != nil {
		return fmt.Errorf("ensure workflow: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE tasks t SET status_id = picked.id, status = picked.category
		FROM (
			SELECT ws.id, ws.category
			FROM tasks t2
			JOIN workflow_statuses cur ON cur.id = t2.status_id
			JOIN workflow_statuses ws ON (
				(t2.project_id IS NOT NULL AND ws.project_id = t2.project_id)
				OR (t2.project_id IS NULL AND ws.project_id IS NULL
					AND ws.freelancer_id = t2.freelancer_id AND ws.client_id IS NOT DISTINCT FROM t2.client_id)
			)
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

// SetPrice sets how the task is billed and, at a fixed price, the price in
// cents (nil clears it). An hourly task is priced by the database.
func (r *Repository) SetPrice(ctx context.Context, id, billing string, cents *int64) error {
	if _, err := r.db.Exec(ctx, `UPDATE tasks SET billing = $2, price_cents = $3 WHERE id = $1`, id, billing, cents); err != nil {
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
	// A project task takes labels of the project's board as well as its own
	// pair's: on a project with several freelancers the board is the
	// project's, and a task waiting for a freelancer has no pair of its own.
	err = tx.QueryRow(ctx, `SELECT count(DISTINCT l.id) FROM labels l
		JOIN tasks t ON t.id = $1
		LEFT JOIN projects pr ON pr.id = t.project_id
		WHERE l.id::text = ANY($2) AND l.client_id IS NOT DISTINCT FROM t.client_id
		  AND (l.freelancer_id = t.freelancer_id OR l.freelancer_id = pr.freelancer_id)`,
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
		JOIN workflow_statuses ws ON `+taskWorkflow+` AND ws.category = $2
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

// Archive archives a task by hand. ErrArchived when it already is.
func (r *Repository) Archive(ctx context.Context, id string) error {
	tag, err := r.db.Exec(ctx, `UPDATE tasks SET archived_at = now(), position = NULL
		WHERE id = $1 AND archived_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("archive task: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrArchived
	}
	return nil
}

// ArchiveFilter narrows the archive and picks a page of it. With and
// ProjectID work as in Filter.
type ArchiveFilter struct {
	With      string
	ProjectID string
	// Page counts from 1; Limit is how many tasks a page holds.
	Page  int
	Limit int
}

// ListArchive returns one page of the archived tasks the user sees, the most
// recently archived first, and how many there are in all.
func (r *Repository) ListArchive(ctx context.Context, userID, role string, f ArchiveFilter) ([]Task, int, error) {
	other := "t.client_id"
	if role == user.RoleClient {
		other = "t.freelancer_id"
	}
	where := `
		WHERE t.archived_at IS NOT NULL AND sees_task($1, t.id, t.freelancer_id, t.client_id)
		  AND ($2 = '' OR (t.client_id IS NOT NULL AND ` + other + `::text = $2)
			OR ($2 = '` + PersonalFilter + `' AND t.client_id IS NULL))
		  AND ($3 = '' OR t.project_id::text = $3)`
	var total int
	if err := r.db.QueryRow(ctx, `SELECT count(*) FROM tasks t`+where, userID, f.With, f.ProjectID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count archive: %w", err)
	}
	rows, err := r.db.Query(ctx, selectTask+where+`
		ORDER BY t.archived_at DESC, t.id DESC
		LIMIT $4 OFFSET $5`, userID, f.With, f.ProjectID, f.Limit, (f.Page-1)*f.Limit)
	if err != nil {
		return nil, 0, fmt.Errorf("list archive: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Task, error) { return scanTask(row) })
	if err != nil {
		return nil, 0, fmt.Errorf("scan archive: %w", err)
	}
	return out, total, nil
}
