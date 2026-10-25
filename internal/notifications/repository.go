package notifications

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository handles PostgreSQL access for notifications; reads and changes
// are scoped to the recipient.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository creates a notifications Repository backed by the given pool.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// Create stores a notification and returns it with its ID, time and the
// actor's name and email. The bell shows it when the recipient wants its
// kind there.
func (r *Repository) Create(ctx context.Context, n Notification) (Notification, error) {
	err := r.db.QueryRow(ctx, `INSERT INTO notifications (user_id, actor_id, kind, task_id, project_id, report_id, subject, detail, in_app)
		VALUES ($1, NULLIF($2, '')::uuid, $3, NULLIF($4, '')::uuid, NULLIF($5, '')::uuid, NULLIF($6, '')::uuid, $7, $8,
			coalesce((SELECT $3 = ANY(p.kinds) FROM app_notification_prefs p WHERE p.user_id = $1), true))
		RETURNING id, created_at,
			coalesce((SELECT name FROM users WHERE id = as_uuid($2)), ''),
			coalesce((SELECT email FROM users WHERE id = as_uuid($2)), '')`,
		n.UserID, n.ActorID, n.Kind, n.TaskID, n.ProjectID, n.ReportID, n.Subject, n.Detail).
		Scan(&n.ID, &n.CreatedAt, &n.ActorName, &n.ActorEmail)
	if err != nil {
		return Notification{}, fmt.Errorf("create notification: %w", err)
	}
	return n, nil
}

// List returns the user's latest notifications in the bell, newest first.
func (r *Repository) List(ctx context.Context, userID string, limit int) ([]Notification, error) {
	rows, err := r.db.Query(ctx, `SELECT n.id, n.user_id, coalesce(n.actor_id::text, ''), n.kind,
			coalesce(n.task_id::text, ''), coalesce(n.project_id::text, ''), coalesce(n.report_id::text, ''),
			n.subject, n.detail, n.read_at, n.created_at, coalesce(a.name, ''), coalesce(a.email, '')
		FROM notifications n
		LEFT JOIN users a ON a.id = n.actor_id
		WHERE n.user_id = $1 AND n.in_app
		ORDER BY n.created_at DESC
		LIMIT $2`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("list notifications: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Notification, error) {
		var n Notification
		err := row.Scan(&n.ID, &n.UserID, &n.ActorID, &n.Kind, &n.TaskID, &n.ProjectID, &n.ReportID,
			&n.Subject, &n.Detail, &n.ReadAt, &n.CreatedAt, &n.ActorName, &n.ActorEmail)
		return n, err
	})
	if err != nil {
		return nil, fmt.Errorf("scan notifications: %w", err)
	}
	return out, nil
}

// UnreadCount returns how many notifications in the bell the user hasn't read.
func (r *Repository) UnreadCount(ctx context.Context, userID string) (int, error) {
	var n int
	err := r.db.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE user_id = $1 AND in_app AND read_at IS NULL`, userID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count unread: %w", err)
	}
	return n, nil
}

// MarkRead marks one of the user's notifications read.
func (r *Repository) MarkRead(ctx context.Context, userID, id string) error {
	tag, err := r.db.Exec(ctx, `UPDATE notifications SET read_at = coalesce(read_at, now())
		WHERE id = as_uuid($1) AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("mark read: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkAllRead marks all of the user's notifications in the bell read; the
// others stay unread, so email still sends them.
func (r *Repository) MarkAllRead(ctx context.Context, userID string) error {
	if _, err := r.db.Exec(ctx, `UPDATE notifications SET read_at = now() WHERE user_id = $1 AND in_app AND read_at IS NULL`, userID); err != nil {
		return fmt.Errorf("mark all read: %w", err)
	}
	return nil
}

// AppKinds returns the kinds the bell shows the user: all of them until they choose.
func (r *Repository) AppKinds(ctx context.Context, userID string) ([]string, error) {
	var kinds []string
	err := r.db.QueryRow(ctx, `SELECT kinds FROM app_notification_prefs WHERE user_id = $1`, userID).Scan(&kinds)
	if errors.Is(err, pgx.ErrNoRows) {
		return slices.Clone(Kinds), nil
	}
	if err != nil {
		return nil, fmt.Errorf("get app kinds: %w", err)
	}
	return kinds, nil
}

// SetAppKinds stores which kinds the bell shows the user.
func (r *Repository) SetAppKinds(ctx context.Context, userID string, kinds []string) error {
	if _, err := r.db.Exec(ctx, `
		INSERT INTO app_notification_prefs (user_id, kinds) VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET kinds = EXCLUDED.kinds, updated_at = now()`, userID, kinds); err != nil {
		return fmt.Errorf("set app kinds: %w", err)
	}
	return nil
}
