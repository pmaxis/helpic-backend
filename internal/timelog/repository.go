package timelog

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository handles PostgreSQL access for time entries. Access to the task
// is checked by the service.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository creates a timelog Repository backed by the given pool.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

const selectEntry = `SELECT e.id, e.task_id, t.title, e.spent_on, e.seconds, e.note, e.running_since, e.created_at,
		u.id, u.name, u.email
	FROM time_entries e
	JOIN tasks t ON t.id = e.task_id
	JOIN users u ON u.id = e.user_id`

func scan(row pgx.Row) (Entry, error) {
	var e Entry
	err := row.Scan(&e.ID, &e.TaskID, &e.TaskTitle, &e.SpentOn, &e.Seconds, &e.Note, &e.RunningSince, &e.CreatedAt,
		&e.User.ID, &e.User.Name, &e.User.Email)
	return e, err
}

// List returns a task's entries, newest day first.
func (r *Repository) List(ctx context.Context, taskID string) ([]Entry, error) {
	rows, err := r.db.Query(ctx, selectEntry+` WHERE e.task_id = $1 ORDER BY e.spent_on DESC, e.created_at DESC`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list time entries: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Entry, error) { return scan(row) })
	if err != nil {
		return nil, fmt.Errorf("scan time entries: %w", err)
	}
	return out, nil
}

// Get loads one entry, or ErrNotFound.
func (r *Repository) Get(ctx context.Context, id string) (Entry, error) {
	e, err := scan(r.db.QueryRow(ctx, selectEntry+` WHERE e.id::text = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Entry{}, ErrNotFound
	}
	if err != nil {
		return Entry{}, fmt.Errorf("get time entry: %w", err)
	}
	return e, nil
}

// Running returns the user's running timer, or ErrNoTimer.
func (r *Repository) Running(ctx context.Context, userID string) (Entry, error) {
	e, err := scan(r.db.QueryRow(ctx, selectEntry+` WHERE e.user_id = $1 AND e.running_since IS NOT NULL`, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Entry{}, ErrNoTimer
	}
	if err != nil {
		return Entry{}, fmt.Errorf("get running timer: %w", err)
	}
	return e, nil
}

// Create adds an entry written by hand and returns its ID.
func (r *Repository) Create(ctx context.Context, taskID, userID string, p Params) (string, error) {
	var id string
	err := r.db.QueryRow(ctx, `INSERT INTO time_entries (task_id, user_id, spent_on, seconds, note)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`, taskID, userID, p.SpentOn, p.Seconds, p.Note).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("create time entry: %w", err)
	}
	return id, nil
}

// Update rewrites an entry's day, time and note.
func (r *Repository) Update(ctx context.Context, id string, p Params) error {
	if _, err := r.db.Exec(ctx, `UPDATE time_entries SET spent_on = $2, seconds = $3, note = $4 WHERE id = $1`,
		id, p.SpentOn, p.Seconds, p.Note); err != nil {
		return fmt.Errorf("update time entry: %w", err)
	}
	return nil
}

// Delete removes an entry.
func (r *Repository) Delete(ctx context.Context, id string) error {
	if _, err := r.db.Exec(ctx, `DELETE FROM time_entries WHERE id = $1`, id); err != nil {
		return fmt.Errorf("delete time entry: %w", err)
	}
	return nil
}

// stopRunning ends the user's running timer, if any, adding the time since it
// started.
const stopRunning = `UPDATE time_entries
	SET seconds = seconds + greatest(0, extract(epoch FROM $2::timestamptz - running_since))::int, running_since = NULL
	WHERE user_id = $1 AND running_since IS NOT NULL
	RETURNING id`

// Start stops the user's running timer, if any, and starts one on the task,
// as a new entry on the given day. It returns the new entry's ID.
func (r *Repository) Start(ctx context.Context, taskID, userID string, day, now time.Time) (string, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err := tx.Exec(ctx, stopRunning, userID, now); err != nil {
		return "", fmt.Errorf("stop timer: %w", err)
	}
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO time_entries (task_id, user_id, spent_on, running_since)
		VALUES ($1, $2, $3, $4) RETURNING id`, taskID, userID, day, now).Scan(&id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		// Another start won the race; its timer is the one running.
		return "", ErrRunning
	}
	if err != nil {
		return "", fmt.Errorf("start timer: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit: %w", err)
	}
	return id, nil
}

// Stop ends the user's running timer and returns its entry's ID, or ErrNoTimer.
func (r *Repository) Stop(ctx context.Context, userID string, now time.Time) (string, error) {
	var id string
	err := r.db.QueryRow(ctx, stopRunning, userID, now).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNoTimer
	}
	if err != nil {
		return "", fmt.Errorf("stop timer: %w", err)
	}
	return id, nil
}
