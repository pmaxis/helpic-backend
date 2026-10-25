package admin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/pmaxis/helpic/internal/ratelimit"
)

// What holds someone back from signing in: an account's devices, one at a
// time, and the rate limits that refuse their attempts for a while.

const (
	// ActionEndSession signs one device out.
	ActionEndSession = "end_session"
	// ActionClearLimit lifts a rate limit before it lifts itself.
	ActionClearLimit = "clear_limit"
)

// ErrSessionNotFound is returned for a session that is gone or another
// account's.
var ErrSessionNotFound = errors.New("session not found")

// ErrLimitNotFound is returned for a limit that has lifted.
var ErrLimitNotFound = errors.New("limit not found")

// Session is a device an account is signed in on.
type Session struct {
	ID         string
	UserAgent  string
	IP         string
	CreatedAt  time.Time
	LastUsedAt time.Time
}

// Sessions returns the account's live sessions, the most recently used
// first. ErrNotFound for no such account, or an admin's.
func (s *Service) Sessions(ctx context.Context, id string) ([]Session, error) {
	return s.repo.Sessions(ctx, id)
}

// EndSession signs the account id out of one device as the admin adminID,
// records it with the reason and returns the account as it is now.
func (s *Service) EndSession(ctx context.Context, adminID, id, sessionID, reason string) (AccountDetail, error) {
	if err := s.repo.EndSession(ctx, adminID, id, sessionID, reason); err != nil {
		return AccountDetail{}, err
	}
	return s.AccountDetail(ctx, id)
}

// Sessions lists an account's live sessions.
func (r *Repository) Sessions(ctx context.Context, id string) ([]Session, error) {
	var exists bool
	err := r.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE id = $1 AND role <> 'admin')`, id).Scan(&exists)
	if isBadUUID(err) || (err == nil && !exists) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find account: %w", err)
	}
	rows, err := r.db.Query(ctx, `
		SELECT id, user_agent, ip, created_at, last_used_at FROM sessions
		WHERE user_id = $1 AND expires_at > now()
		ORDER BY last_used_at DESC`, id)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Session, error) {
		var s Session
		err := row.Scan(&s.ID, &s.UserAgent, &s.IP, &s.CreatedAt, &s.LastUsedAt)
		return s, err
	})
	if err != nil {
		return nil, fmt.Errorf("scan sessions: %w", err)
	}
	return out, nil
}

// EndSession deletes one session of the account and records it, in one
// transaction.
func (r *Repository) EndSession(ctx context.Context, adminID, id, sessionID, reason string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var target string
	err = tx.QueryRow(ctx, `SELECT email FROM users WHERE id = $1 AND role <> 'admin'`, id).Scan(&target)
	if errors.Is(err, pgx.ErrNoRows) || isBadUUID(err) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("find account: %w", err)
	}
	tag, err := tx.Exec(ctx, `DELETE FROM sessions WHERE id = as_uuid($1) AND user_id = $2`, sessionID, id)
	if err != nil {
		return fmt.Errorf("end session: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrSessionNotFound
	}
	if err := record(ctx, tx, adminID, ActionEndSession, target, reason); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Limit is a rate limit refusing someone right now.
type Limit struct {
	Scope string
	Key   string
	// Kind is what Key is, a ratelimit.Key constant; Email and IP are its
	// parts.
	Kind  string
	Email string
	IP    string
	// AccountID and AccountEmail are the account it holds back, when
	// there is one.
	AccountID    *string
	AccountEmail string
	// Burst attempts are allowed at once, then one every Every.
	Burst   int
	Every   time.Duration
	OpensAt time.Time
}

// LimitFilter picks the limits to list.
type LimitFilter struct {
	// Query is part of the email, the address or the account's email.
	Query string
	// AccountID keeps the limits on one account; empty for all.
	AccountID string
}

// Limits returns the rate limits refusing attempts now, the longest first.
func (s *Service) Limits(ctx context.Context, f LimitFilter) ([]Limit, error) {
	return s.repo.Limits(ctx, f)
}

// ClearLimit lifts the limit as the admin adminID and records it.
// ErrLimitNotFound when it has lifted already.
func (s *Service) ClearLimit(ctx context.Context, adminID, scope, key string) error {
	return s.repo.ClearLimit(ctx, adminID, scope, key)
}

// limitsSQL picks the buckets that are empty now, with when they let the
// next attempt through and the account their key names. $1 are the scopes
// keyed by email, $2 by email and address, $3 by user.
const limitsSQL = `
	WITH b AS (
		SELECT scope, key, burst, every_seconds,
			LEAST(burst, tokens + EXTRACT(EPOCH FROM (clock_timestamp() - updated_at)) / every_seconds) AS left_now
		FROM rate_limit_buckets
	)
	SELECT b.scope, b.key, b.burst, b.every_seconds,
		clock_timestamp() + make_interval(secs => (1 - b.left_now) * b.every_seconds),
		u.id, COALESCE(u.email, '')
	FROM b
	LEFT JOIN users u ON u.role <> 'admin' AND (
		(b.scope = ANY($1) AND u.email = b.key)
		OR (b.scope = ANY($2) AND u.email = split_part(b.key, ' ', 1))
		OR (b.scope = ANY($3) AND u.id = as_uuid(b.key)))
	WHERE b.left_now < 1`

// Limits lists the empty buckets.
func (r *Repository) Limits(ctx context.Context, f LimitFilter) ([]Limit, error) {
	q := strings.ToLower(strings.TrimSpace(f.Query))
	rows, err := r.db.Query(ctx, `SELECT * FROM (`+limitsSQL+`) l (scope, key, burst, every_seconds, opens_at, account_id, account_email)
		WHERE ($4 = '' OR account_id = as_uuid($4))
		  AND ($5 = '' OR strpos(lower(key), $5) > 0 OR strpos(lower(account_email), $5) > 0)
		ORDER BY opens_at DESC, scope, key`,
		ratelimit.ScopesKeyedBy(ratelimit.KeyEmail), ratelimit.ScopesKeyedBy(ratelimit.KeyEmailIP), ratelimit.ScopesKeyedBy(ratelimit.KeyUser),
		f.AccountID, q)
	if err != nil {
		return nil, fmt.Errorf("list limits: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Limit, error) {
		var l Limit
		var burst, every float64
		if err := row.Scan(&l.Scope, &l.Key, &burst, &every, &l.OpensAt, &l.AccountID, &l.AccountEmail); err != nil {
			return Limit{}, err
		}
		l.Burst, l.Every = int(burst), time.Duration(every*float64(time.Second))
		l.Kind = ratelimit.KeyOf(l.Scope)
		switch l.Kind {
		case ratelimit.KeyEmail:
			l.Email = l.Key
		case ratelimit.KeyEmailIP:
			l.Email, l.IP, _ = strings.Cut(l.Key, " ")
		case ratelimit.KeyIP:
			l.IP = l.Key
		}
		return l, nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan limits: %w", err)
	}
	return out, nil
}

// ClearLimit deletes the bucket while it is empty, a full one in effect,
// and records it in one transaction. The record names the account, else the email, else the
// address.
func (r *Repository) ClearLimit(ctx context.Context, adminID, scope, key string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var target string
	err = tx.QueryRow(ctx, `
		DELETE FROM rate_limit_buckets
		WHERE scope = $1 AND key = $2
		  AND LEAST(burst, tokens + EXTRACT(EPOCH FROM (clock_timestamp() - updated_at)) / every_seconds) < 1
		RETURNING COALESCE(
			CASE WHEN scope = ANY($3) THEN (SELECT email FROM users WHERE id = as_uuid(key)) END,
			CASE WHEN scope = ANY($4) THEN split_part(key, ' ', 1) END,
			key)`, scope, key, ratelimit.ScopesKeyedBy(ratelimit.KeyUser), ratelimit.ScopesKeyedBy(ratelimit.KeyEmailIP)).Scan(&target)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrLimitNotFound
	}
	if err != nil {
		return fmt.Errorf("clear limit: %w", err)
	}
	if err := record(ctx, tx, adminID, ActionClearLimit, target, scope); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
