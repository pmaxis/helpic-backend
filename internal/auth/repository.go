package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/pmaxis/helpic/internal/user"
)

const uniqueViolationCode = "23505"

// Repository handles PostgreSQL persistence for authentication: creating
// accounts and looking them up by email.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository creates an auth Repository backed by the given pool.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// CreateUser inserts a new account and returns the persisted row. The single
// name typed at sign-up (a person's or a company's) becomes the first name. A
// unique constraint violation on email is translated to ErrEmailExists.
func (r *Repository) CreateUser(ctx context.Context, email, name, role, passwordHash, locale string) (user.User, error) {
	u, err := user.Scan(r.db.QueryRow(ctx, `
		INSERT INTO users (email, first_name, role, password_hash, locale)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+user.Columns, email, name, role, passwordHash, locale))

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolationCode {
		return user.User{}, ErrEmailExists
	}
	if err != nil {
		return user.User{}, fmt.Errorf("create user: %w", err)
	}
	return u, nil
}

// DeleteUser removes an account.
func (r *Repository) DeleteUser(ctx context.Context, id string) error {
	if _, err := r.db.Exec(ctx, `DELETE FROM users WHERE id = $1`, id); err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	return nil
}

// GetByEmail loads an account by its normalized email. Returns user.ErrNotFound
// when there is no match.
func (r *Repository) GetByEmail(ctx context.Context, email string) (user.User, error) {
	u, err := user.Scan(r.db.QueryRow(ctx, `SELECT `+user.Columns+` FROM users WHERE email = $1 AND deleted_at IS NULL`, email))
	if errors.Is(err, pgx.ErrNoRows) {
		return user.User{}, user.ErrNotFound
	}
	if err != nil {
		return user.User{}, fmt.Errorf("get user by email: %w", err)
	}
	return u, nil
}
