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
	// ErrNotParty is returned when anyone but the report's freelancer or
	// client marks it paid.
	ErrNotParty = errors.New("only the report's freelancer or client marks it paid")
	// ErrNotSent is returned when marking paid a report that isn't sent and
	// unpaid.
	ErrNotSent = errors.New("only a sent, unpaid report can be marked paid")
)

// onBothSides reports whether the user is the freelancer or the client of a
// report that has both.
func onBothSides(r Report, userID string) bool {
	return r.Client != nil && (r.Client.ID == userID || r.Freelancer.ID == userID)
}

// CanMarkPaid reports whether the viewer may mark the report paid: either
// side of it, while it's sent and unpaid.
func CanMarkPaid(r Report, viewerID string) bool {
	return onBothSides(r, viewerID) && r.Status == StatusSent
}

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

// MarkPaid is the client saying they paid a sent report, or the freelancer
// saying the money came: it becomes paid, its tasks are archived, and the
// other side is told. There's no taking it back.
func (s *Service) MarkPaid(ctx context.Context, userID, id string) (Report, error) {
	r, err := s.repo.Get(ctx, userID, id)
	if err != nil {
		return Report{}, err
	}
	if !onBothSides(r, userID) {
		return Report{}, ErrNotParty
	}
	other := r.Freelancer.ID
	if userID == r.Freelancer.ID {
		other = r.Client.ID
	}
	archived, err := s.repo.MarkPaid(ctx, r.ID, time.Now())
	if err != nil {
		return Report{}, err
	}
	for _, a := range archived {
		if a.From != "" {
			s.tasks.Record(ctx, a.ID, userID, tasks.EventStatus, map[string]any{"from": a.From, "to": a.To, "category": tasks.StatusDone})
		}
		s.tasks.Record(ctx, a.ID, userID, tasks.EventArchived, map[string]any{"report": r.Title})
	}
	s.record(ctx, r.ID, userID, EventPaid, map[string]any{"tasks": len(archived)})
	s.notify.Notify(ctx, notifications.Notification{
		UserID:    other,
		ActorID:   userID,
		Kind:      notifications.KindReportPaid,
		ReportID:  r.ID,
		ProjectID: soleProject(r),
		Subject:   r.Title,
	})
	return s.read(ctx, userID, id)
}
