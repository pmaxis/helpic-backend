package attachments

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository handles PostgreSQL access for attachments.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository creates an attachments Repository backed by the given pool.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

const selectAttachment = `SELECT a.id, a.task_id, a.file_name, a.content_type, a.size_bytes, a.created_at,
		u.id, u.name, u.email
	FROM attachments a JOIN users u ON u.id = a.uploaded_by`

func scan(row pgx.Row) (Attachment, error) {
	var a Attachment
	err := row.Scan(&a.ID, &a.TaskID, &a.FileName, &a.ContentType, &a.SizeBytes, &a.CreatedAt,
		&a.Uploader.ID, &a.Uploader.Name, &a.Uploader.Email)
	return a, err
}

// List returns a task's files, oldest first.
func (r *Repository) List(ctx context.Context, taskID string) ([]Attachment, error) {
	rows, err := r.db.Query(ctx, selectAttachment+` WHERE a.task_id = $1 ORDER BY a.created_at, a.id`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list attachments: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Attachment, error) { return scan(row) })
	if err != nil {
		return nil, fmt.Errorf("scan attachments: %w", err)
	}
	return out, nil
}

// Get loads one file, or ErrNotFound.
func (r *Repository) Get(ctx context.Context, id string) (Attachment, error) {
	a, err := scan(r.db.QueryRow(ctx, selectAttachment+` WHERE a.id::text = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Attachment{}, ErrNotFound
	}
	if err != nil {
		return Attachment{}, fmt.Errorf("get attachment: %w", err)
	}
	return a, nil
}

// NewID reserves an ID for a file about to be written.
func (r *Repository) NewID(ctx context.Context) (string, error) {
	var id string
	if err := r.db.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&id); err != nil {
		return "", fmt.Errorf("new attachment id: %w", err)
	}
	return id, nil
}

// Create records a stored file.
func (r *Repository) Create(ctx context.Context, a Attachment) error {
	_, err := r.db.Exec(ctx, `INSERT INTO attachments (id, task_id, uploaded_by, file_name, content_type, size_bytes)
		VALUES ($1, $2, $3, $4, $5, $6)`, a.ID, a.TaskID, a.Uploader.ID, a.FileName, a.ContentType, a.SizeBytes)
	if err != nil {
		return fmt.Errorf("create attachment: %w", err)
	}
	return nil
}

// Delete removes a file's record.
func (r *Repository) Delete(ctx context.Context, id string) error {
	if _, err := r.db.Exec(ctx, `DELETE FROM attachments WHERE id = $1`, id); err != nil {
		return fmt.Errorf("delete attachment: %w", err)
	}
	return nil
}

// UsedBytes is the size of every file the user has uploaded.
func (r *Repository) UsedBytes(ctx context.Context, userID string) (int64, error) {
	var n int64
	if err := r.db.QueryRow(ctx, `SELECT coalesce(sum(size_bytes), 0) FROM attachments WHERE uploaded_by = $1`, userID).Scan(&n); err != nil {
		return 0, fmt.Errorf("sum uploads: %w", err)
	}
	return n, nil
}

// IDsOfTask lists a task's file IDs, to remove the files with the task.
func (r *Repository) IDsOfTask(ctx context.Context, taskID string) ([]string, error) {
	rows, err := r.db.Query(ctx, `SELECT id::text FROM attachments WHERE task_id::text = $1`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list attachment ids: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("scan attachment ids: %w", err)
	}
	return ids, nil
}
