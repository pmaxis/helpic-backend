package user

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository handles all PostgreSQL access for reading users.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository creates a user Repository backed by the given pool.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// GetByID loads a user by primary key. Returns ErrNotFound if absent.
func (r *Repository) GetByID(ctx context.Context, id string) (User, error) {
	u, err := Scan(r.db.QueryRow(ctx, `SELECT `+Columns+` FROM users WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("get user by id: %w", err)
	}
	return u, nil
}
