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

// taskRecorder adds to a task's history. Implemented by *tasks.Service.
type taskRecorder interface {
	Record(ctx context.Context, taskID, actorID, kind string, data map[string]any)
}

// archivedTask is a task a payment archived. From is the column it left when
// it wasn't done yet (it's done now), "" when it was.
type archivedTask struct {
	ID   string
	From string
	To   string
}

// MarkPaid marks a sent report paid and archives its tasks: they leave the
// boards for good. A task the freelancer had moved back from done is done
// again first, in its workflow's first done column.
func (r *Repository) MarkPaid(ctx context.Context, id string, at time.Time) ([]archivedTask, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	tag, err := tx.Exec(ctx, `UPDATE reports SET status = 'paid', paid_at = $2
		WHERE id = $1 AND status = 'sent'`, id, at)
	if err != nil {
		return nil, fmt.Errorf("mark report paid: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotSent
	}

	// Each task not archived yet, with the done column it goes to if it
	// isn't done: its project's, or its pair's.
	rows, err := tx.Query(ctx, `SELECT t.id, cur.name, coalesce(done.id::text, ''), coalesce(done.name, '')
		FROM report_items i
		JOIN tasks t ON t.id = i.task_id
		JOIN workflow_statuses cur ON cur.id = t.status_id
		LEFT JOIN LATERAL (
			SELECT ws.id, ws.name FROM workflow_statuses ws
			WHERE ws.category = 'done' AND t.status <> 'done' AND (
				(t.project_id IS NOT NULL AND ws.project_id = t.project_id)
				OR (t.project_id IS NULL AND ws.project_id IS NULL
					AND ws.freelancer_id = t.freelancer_id AND ws.client_id IS NOT DISTINCT FROM t.client_id))
			ORDER BY ws.position LIMIT 1
		) done ON true
		WHERE i.report_id = $1 AND t.archived_at IS NULL`, id)
	if err != nil {
		return nil, fmt.Errorf("list report tasks: %w", err)
	}
	type item struct{ id, from, doneID, doneName string }
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (item, error) {
		var it item
		return it, row.Scan(&it.id, &it.from, &it.doneID, &it.doneName)
	})
	if err != nil {
		return nil, fmt.Errorf("scan report tasks: %w", err)
	}

	archived := make([]archivedTask, 0, len(items))
	for _, it := range items {
		a := archivedTask{ID: it.id}
		if it.doneID != "" {
			if _, err := tx.Exec(ctx, `UPDATE tasks SET status_id = $2, status = 'done',
					completed_at = coalesce(completed_at, now())
				WHERE id = $1`, it.id, it.doneID); err != nil {
				return nil, fmt.Errorf("finish task: %w", err)
			}
			a.From, a.To = it.from, it.doneName
		}
		if _, err := tx.Exec(ctx, `UPDATE tasks SET archived_at = $2, archived_report_id = $3, position = NULL
			WHERE id = $1`, it.id, at, id); err != nil {
			return nil, fmt.Errorf("archive task: %w", err)
		}
		archived = append(archived, a)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return archived, nil
}

// MarkPaid is the client saying they paid a sent report: it becomes paid, its
// tasks are archived, and the freelancer is told. There's no taking it back.
func (s *Service) MarkPaid(ctx context.Context, clientID, id string) (Report, error) {
	r, err := s.repo.Get(ctx, clientID, id)
	if err != nil {
		return Report{}, err
	}
	if r.Client == nil || r.Client.ID != clientID {
		return Report{}, ErrNotClient
	}
	archived, err := s.repo.MarkPaid(ctx, r.ID, time.Now())
	if err != nil {
		return Report{}, err
	}
	for _, a := range archived {
		if a.From != "" {
			s.tasks.Record(ctx, a.ID, clientID, tasks.EventStatus, map[string]any{"from": a.From, "to": a.To, "category": tasks.StatusDone})
		}
		s.tasks.Record(ctx, a.ID, clientID, tasks.EventArchived, map[string]any{"report": r.Title})
	}
	s.record(ctx, r.ID, clientID, EventPaid, map[string]any{"tasks": len(archived)})
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
