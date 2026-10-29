package shares

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository is the data access for task links.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository creates a shares Repository backed by the given pool.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// Get returns the task's link, or ErrNotFound when it has none.
func (r *Repository) Get(ctx context.Context, taskID string) (Share, error) {
	var s Share
	err := r.db.QueryRow(ctx, `SELECT task_id::text, token, created_at FROM task_shares WHERE task_id = $1`, taskID).
		Scan(&s.TaskID, &s.Token, &s.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Share{}, ErrNotFound
	}
	if err != nil {
		return Share{}, fmt.Errorf("get task share: %w", err)
	}
	return s, nil
}

// Create gives the task a link with token, unless it has one already;
// created says which. Either way the link the task has comes back.
func (r *Repository) Create(ctx context.Context, taskID, token, userID string) (s Share, created bool, err error) {
	err = r.db.QueryRow(ctx, `INSERT INTO task_shares (task_id, token, created_by) VALUES ($1, $2, $3)
		ON CONFLICT (task_id) DO NOTHING
		RETURNING task_id::text, token, created_at`, taskID, token, userID).Scan(&s.TaskID, &s.Token, &s.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		s, err = r.Get(ctx, taskID)
		return s, false, err
	}
	if err != nil {
		return Share{}, false, fmt.Errorf("create task share: %w", err)
	}
	return s, true, nil
}

// Delete turns the task's link off; removed is false when it had none.
func (r *Repository) Delete(ctx context.Context, taskID string) (removed bool, err error) {
	tag, err := r.db.Exec(ctx, `DELETE FROM task_shares WHERE task_id = $1`, taskID)
	if err != nil {
		return false, fmt.Errorf("delete task share: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// Task returns the task a link shows, or ErrNotFound.
func (r *Repository) Task(ctx context.Context, token string) (Task, error) {
	var t Task
	err := r.db.QueryRow(ctx, `SELECT t.id::text, t.title, t.description, t.status, ws.name, t.due_on, t.archived_at IS NOT NULL
		FROM task_shares s
		JOIN tasks t ON t.id = s.task_id
		JOIN workflow_statuses ws ON ws.id = t.status_id
		WHERE s.token = $1`, token).
		Scan(&t.ID, &t.Title, &t.Description, &t.Status, &t.StatusName, &t.DueOn, &t.Archived)
	if errors.Is(err, pgx.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	if err != nil {
		return Task{}, fmt.Errorf("get shared task: %w", err)
	}
	return t, nil
}

// Subtasks returns a shared task's subtasks, oldest first.
func (r *Repository) Subtasks(ctx context.Context, taskID string) ([]Subtask, error) {
	rows, err := r.db.Query(ctx, `SELECT t.title, t.status, ws.name
		FROM tasks t JOIN workflow_statuses ws ON ws.id = t.status_id
		WHERE t.parent_id = $1
		ORDER BY t.created_at, t.id`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list shared subtasks: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Subtask, error) {
		var s Subtask
		return s, row.Scan(&s.Title, &s.Status, &s.StatusName)
	})
	if err != nil {
		return nil, fmt.Errorf("scan shared subtasks: %w", err)
	}
	return out, nil
}
