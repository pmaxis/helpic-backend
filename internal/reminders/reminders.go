// Package reminders tells people about due dates: the freelancer the day
// before an open task is due, and both sides the day after it was due while
// it is still open. They go out as notifications (and so to Telegram) from
// the reminder hour on, in the app's time zone, once per task and due date.
package reminders

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/pmaxis/helpic/internal/notifications"
)

// Kinds of reminder, as task_reminders stores them.
const (
	dueSoon = "due_soon"
	overdue = "overdue"
)

// keepFor is how long sent reminders are kept. They are only ever claimed for
// yesterday and tomorrow, so older ones can go.
const keepFor = 7 * 24 * time.Hour

// notifier sends the reminders. Implemented by *notifications.Service.
type notifier interface {
	Notify(ctx context.Context, n notifications.Notification)
}

// Service sends the reminders that are due.
type Service struct {
	repo   *Repository
	notify notifier
	// loc is the app's time zone: due dates are days there.
	loc *time.Location
	// hour is when in the day reminders start going out.
	hour int
	now  func() time.Time
}

// NewService creates a reminders Service.
func NewService(repo *Repository, notify notifier, loc *time.Location, hour int) *Service {
	return &Service{repo: repo, notify: notify, loc: loc, hour: hour, now: time.Now}
}

// WithClock makes the Service read the time from now; for tests.
func (s *Service) WithClock(now func() time.Time) *Service {
	s.now = now
	return s
}

// Run sends the reminders due now: none before the reminder hour, then those
// for tasks due tomorrow and those overdue since yesterday. Run it every few
// minutes; each reminder goes once.
func (s *Service) Run(ctx context.Context) error {
	now := s.now().In(s.loc)
	if now.Hour() < s.hour {
		return nil
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	soon, err := s.repo.Claim(ctx, dueSoon, today.AddDate(0, 0, 1))
	if err != nil {
		return err
	}
	for _, t := range soon {
		// The freelancer does the work: they're the one to remind.
		s.send(ctx, t, notifications.KindTaskDueSoon, t.FreelancerID)
	}

	late, err := s.repo.Claim(ctx, overdue, today.AddDate(0, 0, -1))
	if err != nil {
		return err
	}
	for _, t := range late {
		s.send(ctx, t, notifications.KindTaskOverdue, t.FreelancerID, t.ClientID, t.MemberID)
	}
	return nil
}

func (s *Service) send(ctx context.Context, t Due, kind string, to ...string) {
	for _, userID := range to {
		if userID == "" {
			continue
		}
		s.notify.Notify(ctx, notifications.Notification{
			UserID:    userID,
			Kind:      kind,
			TaskID:    t.TaskID,
			ProjectID: t.ProjectID,
			Subject:   t.Title,
			Detail:    t.DueOn.Format(time.DateOnly),
		})
	}
}

// Cleanup forgets reminders sent long enough ago that they can't be claimed again.
func (s *Service) Cleanup(ctx context.Context) error {
	return s.repo.DeleteSentBefore(ctx, s.now().Add(-keepFor))
}

// --- repository ------------------------------------------------------------------

// Due is an open task a reminder was claimed for, with who to tell.
type Due struct {
	TaskID    string
	Title     string
	ProjectID string
	DueOn     time.Time
	// FreelancerID is empty while a project task waits for a freelancer.
	FreelancerID string
	// ClientID is empty for personal work.
	ClientID string
	// MemberID is the client company's member who set the task, while still
	// in the company; empty otherwise.
	MemberID string
}

// Repository handles PostgreSQL access for reminders.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository creates a reminders Repository backed by the given pool.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// Claim marks a reminder of this kind sent for every open task due on the day
// that hasn't had one, and returns those tasks. A task claimed once is never
// returned again for the same kind and due date.
func (r *Repository) Claim(ctx context.Context, kind string, dueOn time.Time) ([]Due, error) {
	rows, err := r.db.Query(ctx, `
		WITH due AS (
			SELECT id, title, project_id, due_on, freelancer_id, client_id, created_by
			FROM tasks
			WHERE due_on = $2::date AND status IN ('new', 'in_progress') AND archived_at IS NULL
		), claimed AS (
			INSERT INTO task_reminders (task_id, kind, due_on)
			SELECT id, $1, due_on FROM due
			ON CONFLICT DO NOTHING
			RETURNING task_id
		)
		SELECT d.id, d.title, coalesce(d.project_id::text, ''), d.due_on, coalesce(d.freelancer_id::text, ''),
			coalesce(d.client_id::text, ''),
			CASE WHEN d.client_id IS NOT NULL AND d.created_by NOT IN (d.freelancer_id, d.client_id)
				AND acting_client(d.created_by) = d.client_id
			THEN d.created_by::text ELSE '' END
		FROM due d JOIN claimed c ON c.task_id = d.id`, kind, dueOn.Format(time.DateOnly))
	if err != nil {
		return nil, fmt.Errorf("claim %s reminders: %w", kind, err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Due, error) {
		var d Due
		err := row.Scan(&d.TaskID, &d.Title, &d.ProjectID, &d.DueOn, &d.FreelancerID, &d.ClientID, &d.MemberID)
		return d, err
	})
	if err != nil {
		return nil, fmt.Errorf("scan %s reminders: %w", kind, err)
	}
	return out, nil
}

// DeleteSentBefore removes reminders sent before t.
func (r *Repository) DeleteSentBefore(ctx context.Context, t time.Time) error {
	if _, err := r.db.Exec(ctx, `DELETE FROM task_reminders WHERE sent_at < $1`, t); err != nil {
		return fmt.Errorf("delete old reminders: %w", err)
	}
	return nil
}
