package branding

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository handles PostgreSQL access for the look of reports.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository creates a branding Repository backed by the given pool.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// Get returns the user's look.
func (r *Repository) Get(ctx context.Context, userID string) (Branding, error) {
	var b Branding
	err := r.db.QueryRow(ctx, `SELECT brand_color, business_details, coalesce(brand_logo_id::text, '') FROM users WHERE id = $1`, userID).
		Scan(&b.Color, &b.Details, &b.LogoID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Branding{}, ErrNotFound
	}
	if err != nil {
		return Branding{}, fmt.Errorf("get branding: %w", err)
	}
	return b, nil
}

// Set writes the user's color and business details.
func (r *Repository) Set(ctx context.Context, userID string, p Params) error {
	if _, err := r.db.Exec(ctx, `UPDATE users SET brand_color = $2, business_details = $3 WHERE id = $1`, userID, p.Color, p.Details); err != nil {
		return fmt.Errorf("set branding: %w", err)
	}
	return nil
}

// SetLogo makes a new logo the user's, in place of the one they had; nil
// data takes their logo away.
func (r *Repository) SetLogo(ctx context.Context, userID, contentType string, data []byte) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var old *string
	if err := tx.QueryRow(ctx, `SELECT brand_logo_id::text FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&old); err != nil {
		return fmt.Errorf("lock user: %w", err)
	}
	var id *string
	if data != nil {
		if err := tx.QueryRow(ctx, `INSERT INTO brand_logos (owner_id, content_type, data) VALUES ($1, $2, $3) RETURNING id`,
			userID, contentType, data).Scan(&id); err != nil {
			return fmt.Errorf("add logo: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET brand_logo_id = $2 WHERE id = $1`, userID, id); err != nil {
		return fmt.Errorf("set logo: %w", err)
	}
	if old != nil {
		if err := dropUnused(ctx, tx, *old); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// dropUnused deletes a logo nobody shows any more: not its owner, and no
// report they sent.
func dropUnused(ctx context.Context, tx pgx.Tx, id string) error {
	_, err := tx.Exec(ctx, `DELETE FROM brand_logos l WHERE l.id = $1
		AND NOT EXISTS (SELECT 1 FROM users u WHERE u.brand_logo_id = l.id)
		AND NOT EXISTS (SELECT 1 FROM reports r WHERE r.brand_logo_id = l.id)`, id)
	if err != nil {
		return fmt.Errorf("drop unused logo: %w", err)
	}
	return nil
}

// Logo returns a logo's file, or ErrNotFound.
func (r *Repository) Logo(ctx context.Context, id string) (Logo, error) {
	var l Logo
	err := r.db.QueryRow(ctx, `SELECT id, content_type, data, created_at FROM brand_logos WHERE id = as_uuid($1)`, id).
		Scan(&l.ID, &l.ContentType, &l.Data, &l.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Logo{}, ErrNotFound
	}
	if err != nil {
		return Logo{}, fmt.Errorf("get logo: %w", err)
	}
	return l, nil
}
