package reports

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository handles PostgreSQL access for reports. A report is visible to its
// freelancer, and to its client once sent.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository creates a reports Repository backed by the given pool.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

const selectReport = `SELECT r.id, r.title, r.summary, r.kind, r.period_from, r.period_to, r.status, r.sent_at, r.paid_at,
		r.created_at, r.updated_at,
		coalesce((SELECT json_agg(json_build_object('id', coalesce(rp.project_id::text, ''), 'name', rp.name) ORDER BY rp.position)
			FROM report_projects rp WHERE rp.report_id = r.id), '[]'),
		f.id, f.name, f.email, c.id, c.name, c.email, coalesce(r.currency, f.currency),
		(SELECT count(*) FROM report_items i WHERE i.report_id = r.id),
		-- the sum of its prices: live while a draft, frozen once sent; NULL when none has one
		(SELECT sum(CASE WHEN r.status = 'draft' THEN t.price_cents ELSE i.price_cents END)
			FROM report_items i LEFT JOIN tasks t ON t.id = i.task_id WHERE i.report_id = r.id)
	FROM reports r
	JOIN users f ON f.id = r.freelancer_id
	LEFT JOIN users c ON c.id = r.client_id`

// visible keeps the reports the user may see: their own, or sent to them.
const visible = `(r.freelancer_id = $1 OR (r.client_id = $1 AND r.status <> 'draft'))`

func scanReport(row pgx.Row) (Report, error) {
	var (
		r                       Report
		clientID, cName, cEmail *string
		projects                []byte
	)
	err := row.Scan(&r.ID, &r.Title, &r.Summary, &r.Kind, &r.From, &r.To, &r.Status, &r.SentAt, &r.PaidAt,
		&r.CreatedAt, &r.UpdatedAt, &projects,
		&r.Freelancer.ID, &r.Freelancer.Name, &r.Freelancer.Email,
		&clientID, &cName, &cEmail, &r.Currency, &r.ItemCount, &r.ListTotal)
	if err != nil {
		return r, err
	}
	if clientID != nil {
		r.Client = &Party{ID: *clientID, Name: *cName, Email: *cEmail}
	}
	if err := json.Unmarshal(projects, &r.Projects); err != nil {
		return r, fmt.Errorf("decode report projects: %w", err)
	}
	return r, nil
}

// List returns the reports the user may see, newest first.
func (r *Repository) List(ctx context.Context, userID string) ([]Report, error) {
	rows, err := r.db.Query(ctx, selectReport+` WHERE `+visible+`
		ORDER BY coalesce(r.sent_at, r.created_at) DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list reports: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Report, error) { return scanReport(row) })
	if err != nil {
		return nil, fmt.Errorf("scan reports: %w", err)
	}
	return out, nil
}

// Get loads a report the user may see, with its items, or ErrNotFound.
func (r *Repository) Get(ctx context.Context, userID, id string) (Report, error) {
	rep, err := scanReport(r.db.QueryRow(ctx, selectReport+` WHERE r.id::text = $2 AND `+visible, userID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Report{}, ErrNotFound
	}
	if err != nil {
		return Report{}, fmt.Errorf("get report: %w", err)
	}
	// A draft shows the tasks' current prices; a sent report its frozen ones.
	rows, err := r.db.Query(ctx, `SELECT i.id, coalesce(i.task_id::text, ''), i.title, i.project_name, i.status_name, i.completed_on, i.notes,
			CASE WHEN $2 = 'draft' THEN t.price_cents ELSE i.price_cents END
		FROM report_items i LEFT JOIN tasks t ON t.id = i.task_id
		WHERE i.report_id = $1 ORDER BY i.position`, rep.ID, rep.Status)
	if err != nil {
		return Report{}, fmt.Errorf("list report items: %w", err)
	}
	rep.Items, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (Item, error) {
		var it Item
		err := row.Scan(&it.ID, &it.TaskID, &it.Title, &it.ProjectName, &it.StatusName, &it.CompletedOn, &it.Notes, &it.PriceCents)
		return it, err
	})
	if err != nil {
		return Report{}, fmt.Errorf("scan report items: %w", err)
	}
	return rep, nil
}

// Linked reports whether the freelancer and the client work together.
func (r *Repository) Linked(ctx context.Context, freelancerID, clientID string) (bool, error) {
	var ok bool
	err := r.db.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM freelancer_clients WHERE freelancer_id = $1 AND client_id::text = $2)`,
		freelancerID, clientID).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("check link: %w", err)
	}
	return ok, nil
}

// ProjectName returns the name of a project between the freelancer and the
// client ("" = a personal project), or ErrProjectMismatch.
func (r *Repository) ProjectName(ctx context.Context, freelancerID, clientID, projectID string) (string, error) {
	var name string
	err := r.db.QueryRow(ctx, `SELECT name FROM projects
		WHERE id::text = $3 AND freelancer_id = $1 AND client_id IS NOT DISTINCT FROM NULLIF($2, '')::uuid`,
		freelancerID, clientID, projectID).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrProjectMismatch
	}
	if err != nil {
		return "", fmt.Errorf("get project: %w", err)
	}
	return name, nil
}

// Create stores a draft report and snapshots the tasks the freelancer
// completed for the client — or personal tasks, without a client — in the
// period (in its projects, if it has any), leaving out tasks a paid report
// already covers.
func (r *Repository) Create(ctx context.Context, freelancerID string, p CreateParams, projects []ProjectRef) (string, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var id string
	err = tx.QueryRow(ctx, `INSERT INTO reports (freelancer_id, client_id, title, summary, kind, period_from, period_to)
		VALUES ($1, NULLIF($2, '')::uuid, $3, $4, $5, $6, $7) RETURNING id`,
		freelancerID, p.ClientID, p.Title, p.Summary, p.Kind, p.From, p.To).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("create report: %w", err)
	}
	projectIDs := make([]string, 0, len(projects))
	for i, pr := range projects {
		projectIDs = append(projectIDs, pr.ID)
		if _, err := tx.Exec(ctx, `INSERT INTO report_projects (report_id, position, project_id, name) VALUES ($1, $2, $3, $4)`,
			id, i, pr.ID, pr.Name); err != nil {
			return "", fmt.Errorf("add report project: %w", err)
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO report_items (report_id, task_id, title, project_name, status_name, completed_on, price_cents, notes, position)
		SELECT $1, t.id, t.title, coalesce(pr.name, ''), ws.name, t.completed_at::date, t.price_cents, t.description,
			row_number() OVER (ORDER BY t.completed_at, t.title) - 1
		FROM tasks t
		JOIN workflow_statuses ws ON ws.id = t.status_id
		LEFT JOIN projects pr ON pr.id = t.project_id
		WHERE t.freelancer_id = $2 AND t.client_id IS NOT DISTINCT FROM NULLIF($3, '')::uuid AND t.status = 'done'
		  AND t.completed_at::date BETWEEN $4 AND $5
		  AND (cardinality($6::text[]) = 0 OR t.project_id::text = ANY($6))
		  -- a task already paid for in another report isn't billed again
		  AND NOT EXISTS (SELECT 1 FROM report_items pi JOIN reports pr ON pr.id = pi.report_id
			WHERE pi.task_id = t.id AND pr.status = 'paid')`,
		id, freelancerID, p.ClientID, p.From, p.To, projectIDs)
	if err != nil {
		return "", fmt.Errorf("snapshot tasks: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit: %w", err)
	}
	return id, nil
}

// Update rewrites a draft's title and summary, and its kind unless it's "".
func (r *Repository) Update(ctx context.Context, id string, p UpdateParams) error {
	_, err := r.db.Exec(ctx, `UPDATE reports SET title = $2, summary = $3, kind = coalesce(NULLIF($4, ''), kind)
		WHERE id = $1 AND status = 'draft'`, id, p.Title, p.Summary, p.Kind)
	if err != nil {
		return fmt.Errorf("update report: %w", err)
	}
	return nil
}

// SetItemNotes rewrites what a draft says was done in one task.
func (r *Repository) SetItemNotes(ctx context.Context, reportID, itemID, notes string) error {
	tag, err := r.db.Exec(ctx, `UPDATE report_items SET notes = $3 WHERE id::text = $2 AND report_id = $1`, reportID, itemID, notes)
	if err != nil {
		return fmt.Errorf("set item notes: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RemoveItem drops a task from a draft.
func (r *Repository) RemoveItem(ctx context.Context, reportID, itemID string) error {
	tag, err := r.db.Exec(ctx, `DELETE FROM report_items WHERE id::text = $2 AND report_id = $1`, reportID, itemID)
	if err != nil {
		return fmt.Errorf("remove item: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Send marks a draft sent, at the given time, and freezes its prices and
// currency as they are now.
func (r *Repository) Send(ctx context.Context, id string, at time.Time) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	tag, err := tx.Exec(ctx, `UPDATE reports r SET status = 'sent', sent_at = $2, currency = f.currency
		FROM users f WHERE f.id = r.freelancer_id AND r.id = $1 AND r.status = 'draft'`, id, at)
	if err != nil {
		return fmt.Errorf("send report: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrSent
	}
	_, err = tx.Exec(ctx, `UPDATE report_items i SET price_cents = t.price_cents
		FROM tasks t WHERE t.id = i.task_id AND i.report_id = $1`, id)
	if err != nil {
		return fmt.Errorf("freeze report prices: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// Delete removes a report.
func (r *Repository) Delete(ctx context.Context, id string) error {
	if _, err := r.db.Exec(ctx, `DELETE FROM reports WHERE id = $1`, id); err != nil {
		return fmt.Errorf("delete report: %w", err)
	}
	return nil
}
