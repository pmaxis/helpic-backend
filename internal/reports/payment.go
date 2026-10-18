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

// PaidColumn is the done column a paid report's tasks move to. The first payment creates it.
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

// MarkPaid marks a sent report paid and moves each task to the Paid column of
// its own workflow (the project's, or the pair's when the task is outside a
// project), making that column right after the last done one if the board has
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

	rows, err := tx.Query(ctx, `SELECT t.id, ws.name, coalesce(t.project_id::text, '') FROM report_items i
		JOIN tasks t ON t.id = i.task_id
		JOIN workflow_statuses ws ON ws.id = t.status_id
		WHERE i.report_id = $1`, id)
	if err != nil {
		return nil, fmt.Errorf("list report tasks: %w", err)
	}
	type item struct {
		id, from, projectID string
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (item, error) {
		var it item
		return it, row.Scan(&it.id, &it.from, &it.projectID)
	})
	if err != nil {
		return nil, fmt.Errorf("scan report tasks: %w", err)
	}
	paidByProject := map[string]string{}
	var moved []movedTask
	for _, it := range items {
		paidID, ok := paidByProject[it.projectID]
		if !ok {
			paidID, err = paidColumn(ctx, tx, freelancerID, clientID, it.projectID)
			if err != nil {
				return nil, err
			}
			paidByProject[it.projectID] = paidID
		}
		tag, err := tx.Exec(ctx, `UPDATE tasks SET status_id = $2, status = 'done', position = NULL,
				completed_at = coalesce(completed_at, now())
			WHERE id = $1 AND status_id <> $2`, it.id, paidID)
		if err != nil {
			return nil, fmt.Errorf("move tasks to paid: %w", err)
		}
		if tag.RowsAffected() > 0 {
			moved = append(moved, movedTask{ID: it.id, From: it.from})
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return moved, nil
}

// paidColumn is a workflow's Paid column, created after its last done column
// the first time a payment needs it. projectID "" is the pair's workflow.
func paidColumn(ctx context.Context, tx pgx.Tx, freelancerID string, clientID *string, projectID string) (string, error) {
	var err error
	if projectID != "" {
		_, err = tx.Exec(ctx, `SELECT ensure_project_workflow($1::uuid)`, projectID)
	} else {
		_, err = tx.Exec(ctx, `SELECT ensure_workflow($1, $2)`, freelancerID, clientID)
	}
	if err != nil {
		return "", fmt.Errorf("ensure workflow: %w", err)
	}
	var id string
	err = tx.QueryRow(ctx, `SELECT id FROM workflow_statuses
		WHERE freelancer_id = $1 AND client_id IS NOT DISTINCT FROM $2
		  AND project_id IS NOT DISTINCT FROM NULLIF($3, '')::uuid
		  AND category = 'done' AND lower(name) = lower($4)
		ORDER BY position LIMIT 1`, freelancerID, clientID, projectID, PaidColumn).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return createPaidColumn(ctx, tx, freelancerID, clientID, projectID)
	}
	if err != nil {
		return "", fmt.Errorf("find paid column: %w", err)
	}
	return id, nil
}

// createPaidColumn adds the Paid column right after a workflow's last done one.
func createPaidColumn(ctx context.Context, tx pgx.Tx, freelancerID string, clientID *string, projectID string) (string, error) {
	var after int
	err := tx.QueryRow(ctx, `SELECT coalesce(max(position) FILTER (WHERE category = 'done'), max(position), -1)
		FROM workflow_statuses
		WHERE freelancer_id = $1 AND client_id IS NOT DISTINCT FROM $2
		  AND project_id IS NOT DISTINCT FROM NULLIF($3, '')::uuid`, freelancerID, clientID, projectID).Scan(&after)
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `UPDATE workflow_statuses SET position = position + 1
		WHERE freelancer_id = $1 AND client_id IS NOT DISTINCT FROM $2
		  AND project_id IS NOT DISTINCT FROM NULLIF($3, '')::uuid
		  AND position > $4`, freelancerID, clientID, projectID, after); err != nil {
		return "", err
	}
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO workflow_statuses (freelancer_id, client_id, project_id, name, category, position)
		VALUES ($1, $2, NULLIF($3, '')::uuid, $4, 'done', $5)
		RETURNING id`, freelancerID, clientID, projectID, PaidColumn, after+1).Scan(&id)
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
