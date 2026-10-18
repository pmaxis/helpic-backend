package board

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository handles PostgreSQL access for workflow statuses (board columns)
// and the order of tasks in them.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository creates a board Repository backed by the given pool.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

const columnFields = `ws.id, ws.freelancer_id, coalesce(ws.client_id::text, ''), coalesce(ws.project_id::text, ''),
	ws.name, ws.category, ws.position`

// workflowOf is the freelancer, client and project a workflow belongs to.
const workflowOf = `freelancer_id = $1::uuid AND client_id IS NOT DISTINCT FROM NULLIF($2, '')::uuid
	AND project_id IS NOT DISTINCT FROM NULLIF($3, '')::uuid`

func scanColumn(row pgx.Row) (Column, error) {
	var c Column
	err := row.Scan(&c.ID, &c.Pair.FreelancerID, &c.Pair.ClientID, &c.Pair.ProjectID, &c.Name, &c.Category, &c.Position)
	c.ParticipantsCanMove = OpenToParticipants(c.Category)
	return c, err
}

// ensure makes sure a workflow's default columns exist.
func (r *Repository) ensure(ctx context.Context, p Pair) error {
	var err error
	if p.ProjectID != "" {
		_, err = r.db.Exec(ctx, `SELECT ensure_project_workflow($1::uuid)`, p.ProjectID)
	} else {
		_, err = r.db.Exec(ctx, `SELECT ensure_workflow($1::uuid, NULLIF($2, '')::uuid)`, p.FreelancerID, p.ClientID)
	}
	if err != nil {
		return fmt.Errorf("ensure workflow: %w", err)
	}
	return nil
}

// Columns returns a workflow's columns in order, creating the defaults the
// first time. An empty ProjectID is the pair's workflow.
func (r *Repository) Columns(ctx context.Context, p Pair) ([]Column, error) {
	if err := r.ensure(ctx, p); err != nil {
		return nil, err
	}
	rows, err := r.db.Query(ctx, `SELECT `+columnFields+` FROM workflow_statuses ws
		WHERE ws.freelancer_id::text = $1 AND ws.client_id IS NOT DISTINCT FROM NULLIF($2, '')::uuid
		  AND ws.project_id IS NOT DISTINCT FROM NULLIF($3, '')::uuid
		ORDER BY ws.position, ws.created_at`, p.FreelancerID, p.ClientID, p.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("list columns: %w", err)
	}
	cols, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Column, error) { return scanColumn(row) })
	if err != nil {
		return nil, fmt.Errorf("scan columns: %w", err)
	}
	return cols, nil
}

// Column returns one column, or ErrNotFound.
func (r *Repository) Column(ctx context.Context, id string) (Column, error) {
	c, err := scanColumn(r.db.QueryRow(ctx, `SELECT `+columnFields+` FROM workflow_statuses ws WHERE ws.id::text = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Column{}, ErrNotFound
	}
	if err != nil {
		return Column{}, fmt.Errorf("get column: %w", err)
	}
	return c, nil
}

// ProjectColumns lists the columns of every project on a pair. The pair's own
// workflow (tasks outside a project) is not included.
func (r *Repository) ProjectColumns(ctx context.Context, p Pair) ([]Column, error) {
	rows, err := r.db.Query(ctx, `SELECT `+columnFields+` FROM workflow_statuses ws
		WHERE ws.freelancer_id::text = $1 AND ws.client_id IS NOT DISTINCT FROM NULLIF($2, '')::uuid
		  AND ws.project_id IS NOT NULL
		ORDER BY ws.position, ws.created_at`, p.FreelancerID, p.ClientID)
	if err != nil {
		return nil, fmt.Errorf("list project columns: %w", err)
	}
	cols, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Column, error) { return scanColumn(row) })
	if err != nil {
		return nil, fmt.Errorf("scan project columns: %w", err)
	}
	return cols, nil
}

// Create appends a column to the end of a workflow.
func (r *Repository) Create(ctx context.Context, p Pair, name, bucket string) error {
	_, err := r.db.Exec(ctx, `INSERT INTO workflow_statuses (freelancer_id, client_id, project_id, name, category, position)
		SELECT $1::uuid, NULLIF($2, '')::uuid, NULLIF($3, '')::uuid, $4, $5, coalesce(max(position) + 1, 0)
		FROM workflow_statuses
		WHERE `+workflowOf,
		p.FreelancerID, p.ClientID, p.ProjectID, name, bucket)
	if err != nil {
		return fmt.Errorf("create column: %w", err)
	}
	return nil
}

// Rename changes a column's name. Its bucket, and the tasks in it, stay as they are.
func (r *Repository) Rename(ctx context.Context, id, name string) error {
	if _, err := r.db.Exec(ctx, `UPDATE workflow_statuses SET name = $2 WHERE id::text = $1`, id, name); err != nil {
		return fmt.Errorf("rename column: %w", err)
	}
	return nil
}

// Update renames a column and sets its bucket; the tasks in it take that bucket.
func (r *Repository) Update(ctx context.Context, id, name, bucket string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `UPDATE workflow_statuses SET name = $2, category = $3 WHERE id::text = $1`, id, name, bucket); err != nil {
		return fmt.Errorf("update column: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE tasks SET status = $2,
			completed_at = CASE WHEN $2 = 'done' THEN coalesce(completed_at, now()) END
		WHERE status_id::text = $1 AND status <> $2`, id, bucket); err != nil {
		return fmt.Errorf("recategorise tasks: %w", err)
	}
	return tx.Commit(ctx)
}

// TaskCount returns how many tasks are in a column.
func (r *Repository) TaskCount(ctx context.Context, id string) (int, error) {
	var n int
	if err := r.db.QueryRow(ctx, `SELECT count(*) FROM tasks WHERE status_id::text = $1`, id).Scan(&n); err != nil {
		return 0, fmt.Errorf("count tasks: %w", err)
	}
	return n, nil
}

// Delete removes a column, first moving its tasks to fallbackID ("" when the
// column is empty).
func (r *Repository) Delete(ctx context.Context, id, fallbackID string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if fallbackID != "" {
		if _, err := tx.Exec(ctx, `UPDATE tasks t SET status_id = ws.id, status = ws.category, position = NULL,
				completed_at = CASE WHEN ws.category = 'done' THEN coalesce(t.completed_at, now()) ELSE t.completed_at END
			FROM workflow_statuses ws
			WHERE t.status_id::text = $1 AND ws.id::text = $2`, id, fallbackID); err != nil {
			return fmt.Errorf("move tasks out: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM workflow_statuses WHERE id::text = $1`, id); err != nil {
		return fmt.Errorf("delete column: %w", err)
	}
	return tx.Commit(ctx)
}

// Reorder sets the positions of a pair's columns to their order in ids.
func (r *Repository) Reorder(ctx context.Context, p Pair, ids []string) error {
	_, err := r.db.Exec(ctx, `UPDATE workflow_statuses w SET position = o.ord - 1
		FROM unnest($2::text[]) WITH ORDINALITY AS o(id, ord)
		WHERE w.id::text = o.id AND w.freelancer_id = $1::uuid
		  AND w.client_id IS NOT DISTINCT FROM NULLIF($3, '')::uuid
		  AND w.project_id IS NOT DISTINCT FROM NULLIF($4, '')::uuid`,
		p.FreelancerID, ids, p.ClientID, p.ProjectID)
	if err != nil {
		return fmt.Errorf("reorder columns: %w", err)
	}
	return nil
}

// ProjectAuthor returns the user who created a project, or "" when it is gone.
func (r *Repository) ProjectAuthor(ctx context.Context, projectID string) (string, error) {
	var id string
	err := r.db.QueryRow(ctx, `SELECT created_by::text FROM projects WHERE id::text = $1`, projectID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("project author: %w", err)
	}
	return id, nil
}

// PlaceAll stores the order of the tasks in a column.
func (r *Repository) PlaceAll(ctx context.Context, columnID string, taskIDs []string) error {
	_, err := r.db.Exec(ctx, `UPDATE tasks t SET position = o.ord - 1
		FROM unnest($2::text[]) WITH ORDINALITY AS o(id, ord)
		WHERE t.id::text = o.id AND t.status_id::text = $1`, columnID, taskIDs)
	if err != nil {
		return fmt.Errorf("order tasks: %w", err)
	}
	return nil
}

// Linked reports whether the freelancer and the client work together.
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

// ProjectPair returns the pair of a project the user takes part in, or ErrNoBoard.
func (r *Repository) ProjectPair(ctx context.Context, userID, projectID string) (Pair, error) {
	var p Pair
	err := r.db.QueryRow(ctx, `SELECT freelancer_id, coalesce(client_id::text, ''), id::text, created_by FROM projects
		WHERE id::text = $1 AND sees_project($2::uuid, id)`, projectID, userID).
		Scan(&p.FreelancerID, &p.ClientID, &p.ProjectID, &p.AuthorID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Pair{}, ErrNoBoard
	}
	if err != nil {
		return Pair{}, fmt.Errorf("get project pair: %w", err)
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
