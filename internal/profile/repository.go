// Package profile lets a signed-in user change their own account: display
// name, email, password and billing currency.
package profile

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/pmaxis/helpic/internal/auth"
	"github.com/pmaxis/helpic/internal/user"
)

const uniqueViolationCode = "23505"

// Repository writes account changes for one user at a time.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository creates a profile Repository backed by the given pool.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// Get loads the user, or user.ErrNotFound.
func (r *Repository) Get(ctx context.Context, id string) (user.User, error) {
	return r.updateOne(ctx, `SELECT `+user.Columns+` FROM users WHERE id = $1`, id)
}

// SetName changes the first and last name.
func (r *Repository) SetName(ctx context.Context, id, first, last string) (user.User, error) {
	return r.updateOne(ctx, `UPDATE users SET first_name = $2, last_name = $3 WHERE id = $1 RETURNING `+user.Columns, id, first, last)
}

// SetEmail changes the sign-in email; a taken email yields auth.ErrEmailExists.
func (r *Repository) SetEmail(ctx context.Context, id, email string) (user.User, error) {
	u, err := r.updateOne(ctx, `UPDATE users SET email = $2 WHERE id = $1 RETURNING `+user.Columns, id, email)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolationCode {
		return user.User{}, auth.ErrEmailExists
	}
	return u, err
}

// SetPasswordHash replaces the stored password hash.
func (r *Repository) SetPasswordHash(ctx context.Context, id, hash string) (user.User, error) {
	return r.updateOne(ctx, `UPDATE users SET password_hash = $2 WHERE id = $1 RETURNING `+user.Columns, id, hash)
}

// SetLocale changes the user's language.
func (r *Repository) SetLocale(ctx context.Context, id, locale string) (user.User, error) {
	return r.updateOne(ctx, `UPDATE users SET locale = $2 WHERE id = $1 RETURNING `+user.Columns, id, locale)
}

// SetCurrency changes the billing currency.
func (r *Repository) SetCurrency(ctx context.Context, id, currency string) (user.User, error) {
	return r.updateOne(ctx, `UPDATE users SET currency = $2 WHERE id = $1 RETURNING `+user.Columns, id, currency)
}

// HasPricedTasks reports whether the freelancer has a task with a price.
func (r *Repository) HasPricedTasks(ctx context.Context, userID string) (bool, error) {
	var priced bool
	err := r.db.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM tasks WHERE freelancer_id = $1 AND price_cents IS NOT NULL)`, userID).Scan(&priced)
	if err != nil {
		return false, fmt.Errorf("check priced tasks: %w", err)
	}
	return priced, nil
}

func (r *Repository) updateOne(ctx context.Context, q string, args ...any) (user.User, error) {
	u, err := user.Scan(r.db.QueryRow(ctx, q, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return user.User{}, user.ErrNotFound
	}
	if err != nil {
		return user.User{}, fmt.Errorf("profile query: %w", err)
	}
	return u, nil
}
