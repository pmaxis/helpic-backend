package reports

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/pmaxis/helpic/internal/notifications"
	"github.com/pmaxis/helpic/internal/tasks"
)

var (
	// ErrNotClient is returned when anyone but the report's client marks it
	// paid.
	ErrNotClient = errors.New("only the client marks a report paid")
	// ErrNotSent is returned when marking paid a report that isn't sent and
	// unpaid.
	ErrNotSent = errors.New("only a sent, unpaid report can be marked paid")
)

// PaidColumn is the board column (a status of the done category) that a paid
// report's tasks move to; it is made on the first payment.
const PaidColumn = "Paid"

// taskRecorder adds to a task's history. Implemented by *tasks.Service.
type taskRecorder interface {
	Record(ctx context.Context, taskID, actorID, kind string, data map[string]any)
}

// movedTask is a task a payment moved to the Paid column.
type movedTask struct {
	ID   string
	From string
}

// MarkPaid marks a sent report paid and moves its tasks to the pair's Paid
// column, making the column right after the last done one if the board has
// none. It returns the tasks it moved, with the status each came from.
func (r *Repository) MarkPaid(ctx context.Context, id string, at time.Time) ([]movedTask, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var freelancerID string
	var clientID *string
	err = tx.QueryRow(ctx, `UPDATE reports SET status = 'paid', paid_at = $2
		WHERE id = $1 AND status = 'sent' RETURNING freelancer_id, client_id`, id, at).Scan(&freelancerID, &clientID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotSent
	}
	if err != nil {
		return nil, fmt.Errorf("mark report paid: %w", err)
	}

	// The same lock ensure_workflow takes, so columns don't change under us.
	if _, err := tx.Exec(ctx, `SELECT ensure_workflow($1, $2)`, freelancerID, clientID); err != nil {
		return nil, fmt.Errorf("ensure workflow: %w", err)
	}
	var paidID string
	err = tx.QueryRow(ctx, `SELECT id FROM workflow_statuses
		WHERE freelancer_id = $1 AND client_id IS NOT DISTINCT FROM $2 AND category = 'done' AND lower(name) = lower($3)
		ORDER BY position LIMIT 1`, freelancerID, clientID, PaidColumn).Scan(&paidID)
	if errors.Is(err, pgx.ErrNoRows) {
		paidID, err = createPaidColumn(ctx, tx, freelancerID, clientID)
	}
	if err != nil {
		return nil, fmt.Errorf("find paid column: %w", err)
	}

	rows, err := tx.Query(ctx, `SELECT t.id, ws.name FROM report_items i
		JOIN tasks t ON t.id = i.task_id
		JOIN workflow_statuses ws ON ws.id = t.status_id
		WHERE i.report_id = $1 AND t.status_id <> $2`, id, paidID)
	if err != nil {
		return nil, fmt.Errorf("list report tasks: %w", err)
	}
	moved, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (movedTask, error) {
		var m movedTask
		return m, row.Scan(&m.ID, &m.From)
	})
	if err != nil {
		return nil, fmt.Errorf("scan report tasks: %w", err)
	}
	ids := make([]string, len(moved))
	for i, m := range moved {
		ids[i] = m.ID
	}
	if _, err := tx.Exec(ctx, `UPDATE tasks SET status_id = $2, status = 'done', position = NULL,
			completed_at = coalesce(completed_at, now())
		WHERE id = ANY($1::uuid[])`, ids, paidID); err != nil {
		return nil, fmt.Errorf("move tasks to paid: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return moved, nil
}

// createPaidColumn adds the Paid column right after the pair's last done one.
func createPaidColumn(ctx context.Context, tx pgx.Tx, freelancerID string, clientID *string) (string, error) {
	var after int
	err := tx.QueryRow(ctx, `SELECT coalesce(max(position) FILTER (WHERE category = 'done'), max(position), -1)
		FROM workflow_statuses WHERE freelancer_id = $1 AND client_id IS NOT DISTINCT FROM $2`, freelancerID, clientID).Scan(&after)
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `UPDATE workflow_statuses SET position = position + 1
		WHERE freelancer_id = $1 AND client_id IS NOT DISTINCT FROM $2 AND position > $3`, freelancerID, clientID, after); err != nil {
		return "", err
	}
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO workflow_statuses (freelancer_id, client_id, name, category, position)
		VALUES ($1, $2, $3, 'done', $4) RETURNING id`, freelancerID, clientID, PaidColumn, after+1).Scan(&id)
	return id, err
}

// MarkPaid is the client saying they paid a sent report: it becomes paid, its
// tasks move to the Paid column, and the freelancer is told.
func (s *Service) MarkPaid(ctx context.Context, clientID, id string) (Report, error) {
	r, err := s.repo.Get(ctx, clientID, id)
	if err != nil {
		return Report{}, err
	}
	if r.Client == nil || r.Client.ID != clientID {
		return Report{}, ErrNotClient
	}
	moved, err := s.repo.MarkPaid(ctx, r.ID, time.Now())
	if err != nil {
		return Report{}, err
	}
	for _, m := range moved {
		s.tasks.Record(ctx, m.ID, clientID, tasks.EventStatus, map[string]any{"from": m.From, "to": PaidColumn, "category": tasks.StatusDone})
	}
	s.record(ctx, r.ID, clientID, EventPaid, map[string]any{"tasks": len(moved)})
	s.notify.Notify(ctx, notifications.Notification{
		UserID:    r.Freelancer.ID,
		ActorID:   clientID,
		Kind:      notifications.KindReportPaid,
		ReportID:  r.ID,
		ProjectID: soleProject(r),
		Subject:   r.Title,
	})
	return s.repo.Get(ctx, clientID, id)
}
