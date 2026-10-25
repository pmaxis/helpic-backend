package comments

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository handles PostgreSQL access for comments.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository creates a comments Repository backed by the given pool.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

const selectComment = `SELECT c.id, c.task_id, c.body, c.created_at, c.updated_at, u.id, u.name, u.email
	FROM comments c JOIN users u ON u.id = c.author_id`

func scan(row pgx.Row) (Comment, error) {
	var c Comment
	err := row.Scan(&c.ID, &c.TaskID, &c.Body, &c.CreatedAt, &c.UpdatedAt, &c.Author.ID, &c.Author.Name, &c.Author.Email)
	return c, err
}

// List returns a task's comments, oldest first.
func (r *Repository) List(ctx context.Context, taskID string) ([]Comment, error) {
	rows, err := r.db.Query(ctx, selectComment+` WHERE c.task_id = $1 ORDER BY c.created_at, c.id`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list comments: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Comment, error) { return scan(row) })
	if err != nil {
		return nil, fmt.Errorf("scan comments: %w", err)
	}
	return out, nil
}

// Get loads one comment, or ErrNotFound.
func (r *Repository) Get(ctx context.Context, id string) (Comment, error) {
	c, err := scan(r.db.QueryRow(ctx, selectComment+` WHERE c.id = as_uuid($1)`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Comment{}, ErrNotFound
	}
	if err != nil {
		return Comment{}, fmt.Errorf("get comment: %w", err)
	}
	return c, nil
}

// Create adds a comment and returns its ID.
func (r *Repository) Create(ctx context.Context, taskID, authorID, body string) (string, error) {
	var id string
	err := r.db.QueryRow(ctx, `INSERT INTO comments (task_id, author_id, body) VALUES ($1, $2, $3) RETURNING id`,
		taskID, authorID, body).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("create comment: %w", err)
	}
	return id, nil
}

// Update rewrites a comment's text.
func (r *Repository) Update(ctx context.Context, id, body string) error {
	if _, err := r.db.Exec(ctx, `UPDATE comments SET body = $2 WHERE id = $1`, id, body); err != nil {
		return fmt.Errorf("update comment: %w", err)
	}
	return nil
}

// Delete removes a comment.
func (r *Repository) Delete(ctx context.Context, id string) error {
	if _, err := r.db.Exec(ctx, `DELETE FROM comments WHERE id = $1`, id); err != nil {
		return fmt.Errorf("delete comment: %w", err)
	}
	return nil
}
