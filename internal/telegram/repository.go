package telegram

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository handles PostgreSQL access for connected chats and connect codes.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository creates a telegram Repository backed by the given pool.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// linkColumns reads a telegram_links row with its user's language.
const linkColumns = `user_id, chat_id, username, kinds, linked_at,
	coalesce((SELECT u.locale FROM users u WHERE u.id = telegram_links.user_id), 'en')`

func scanLink(row pgx.Row) (Link, error) {
	var l Link
	err := row.Scan(&l.UserID, &l.ChatID, &l.Username, &l.Kinds, &l.LinkedAt, &l.Locale)
	return l, err
}

// Get returns the user's connected chat. ErrNotConnected when there is none.
func (r *Repository) Get(ctx context.Context, userID string) (Link, error) {
	l, err := scanLink(r.db.QueryRow(ctx, `SELECT `+linkColumns+` FROM telegram_links WHERE user_id = $1`, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Link{}, ErrNotConnected
	}
	if err != nil {
		return Link{}, fmt.Errorf("get telegram link: %w", err)
	}
	return l, nil
}

// NewCode stores a connect code for the user, replacing any earlier one.
func (r *Repository) NewCode(ctx context.Context, userID string, hash []byte, expiresAt time.Time) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `DELETE FROM telegram_link_codes WHERE user_id = $1 OR expires_at <= now()`, userID); err != nil {
		return fmt.Errorf("clear telegram codes: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO telegram_link_codes (code_hash, user_id, expires_at) VALUES ($1, $2, $3)`,
		hash, userID, expiresAt); err != nil {
		return fmt.Errorf("create telegram code: %w", err)
	}
	return tx.Commit(ctx)
}

// TakeCode uses up a connect code and returns the user it was for, their
// name and language. ok is false for an unknown or expired code.
func (r *Repository) TakeCode(ctx context.Context, hash []byte) (userID, name, locale string, ok bool, err error) {
	var live bool
	err = r.db.QueryRow(ctx, `
		DELETE FROM telegram_link_codes c USING users u
		WHERE c.code_hash = $1 AND u.id = c.user_id
		RETURNING c.user_id, u.name, u.locale, c.expires_at > now()`, hash).Scan(&userID, &name, &locale, &live)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", false, nil
	}
	if err != nil {
		return "", "", "", false, fmt.Errorf("take telegram code: %w", err)
	}
	return userID, name, locale, live, nil
}

// Connect ties the chat to the user. Reconnecting keeps the kinds they chose.
func (r *Repository) Connect(ctx context.Context, userID string, chatID int64, username string, kinds []string) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO telegram_links (user_id, chat_id, username, kinds) VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id) DO UPDATE SET chat_id = EXCLUDED.chat_id, username = EXCLUDED.username, linked_at = now()`,
		userID, chatID, username, kinds)
	if err != nil {
		return fmt.Errorf("connect telegram: %w", err)
	}
	return nil
}

// SetKinds changes which notification kinds go to the user's chat.
// ErrNotConnected when there is none.
func (r *Repository) SetKinds(ctx context.Context, userID string, kinds []string) (Link, error) {
	l, err := scanLink(r.db.QueryRow(ctx, `UPDATE telegram_links SET kinds = $2 WHERE user_id = $1 RETURNING `+linkColumns, userID, kinds))
	if errors.Is(err, pgx.ErrNoRows) {
		return Link{}, ErrNotConnected
	}
	if err != nil {
		return Link{}, fmt.Errorf("set telegram kinds: %w", err)
	}
	return l, nil
}

// Disconnect removes the user's chat and returns it. ErrNotConnected when
// there was none.
func (r *Repository) Disconnect(ctx context.Context, userID string) (Link, error) {
	l, err := scanLink(r.db.QueryRow(ctx, `DELETE FROM telegram_links WHERE user_id = $1 RETURNING `+linkColumns, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Link{}, ErrNotConnected
	}
	if err != nil {
		return Link{}, fmt.Errorf("disconnect telegram: %w", err)
	}
	return l, nil
}

// DisconnectChat removes every account connected to the chat and returns how
// many there were.
func (r *Repository) DisconnectChat(ctx context.Context, chatID int64) (int64, error) {
	tag, err := r.db.Exec(ctx, `DELETE FROM telegram_links WHERE chat_id = $1`, chatID)
	if err != nil {
		return 0, fmt.Errorf("disconnect telegram chat: %w", err)
	}
	return tag.RowsAffected(), nil
}

// DeleteExpiredCodes removes connect codes nobody used in time.
func (r *Repository) DeleteExpiredCodes(ctx context.Context) error {
	if _, err := r.db.Exec(ctx, `DELETE FROM telegram_link_codes WHERE expires_at <= now()`); err != nil {
		return fmt.Errorf("delete expired telegram codes: %w", err)
	}
	return nil
}
