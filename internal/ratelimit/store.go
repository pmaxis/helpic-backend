package ratelimit

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the token buckets shared by every copy of the API. The rows live
// in Postgres; two servers therefore spend one budget, and a restart does
// not hand the budget out again.
type Store struct {
	db    *pgxpool.Pool
	scope string
	burst float64
	every time.Duration
}

// NewStore creates a Store. scope keeps its buckets apart from another
// limit's. burst and every are the same numbers New takes.
func NewStore(db *pgxpool.Pool, scope string, burst int, every time.Duration) *Store {
	return &Store{db: db, scope: scope, burst: float64(burst), every: every}
}

// Allow takes a token for key when the bucket has one.
func (s *Store) Allow(ctx context.Context, key string) (bool, time.Duration, error) {
	return s.mutate(ctx, key, spendIfAvailable)
}

// Check reports whether key has a token, without taking it.
func (s *Store) Check(ctx context.Context, key string) (bool, time.Duration, error) {
	return s.mutate(ctx, key, spendNone)
}

// Take uses up a token for key.
func (s *Store) Take(ctx context.Context, key string) error {
	_, _, err := s.mutate(ctx, key, spendAlways)
	return err
}

const (
	spendNone = iota
	spendIfAvailable
	spendAlways
)

// mutate refills the bucket and spends a token the way the caller asked.
func (s *Store) mutate(ctx context.Context, key string, spend int) (bool, time.Duration, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, 0, fmt.Errorf("begin rate limit: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err := tx.Exec(ctx, `INSERT INTO rate_limit_buckets (scope, key, tokens, updated_at, burst, every_seconds)
		VALUES ($1, $2, $3, clock_timestamp(), $3, $4)
		ON CONFLICT (scope, key) DO NOTHING`, s.scope, key, s.burst, s.every.Seconds()); err != nil {
		return false, 0, fmt.Errorf("open rate limit bucket: %w", err)
	}

	var tokens float64
	var updated time.Time
	err = tx.QueryRow(ctx, `SELECT tokens, updated_at FROM rate_limit_buckets
		WHERE scope = $1 AND key = $2 FOR UPDATE`, s.scope, key).Scan(&tokens, &updated)
	if err != nil {
		return false, 0, fmt.Errorf("lock rate limit bucket: %w", err)
	}
	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return false, 0, fmt.Errorf("rate limit clock: %w", err)
	}

	elapsed := now.Sub(updated).Seconds()
	if elapsed < 0 {
		elapsed = 0
	}
	refilled := math.Min(s.burst, tokens+elapsed/s.every.Seconds())
	ok := refilled >= 1
	next := refilled
	if spend == spendAlways || (spend == spendIfAvailable && ok) {
		next = math.Max(0, refilled-1)
	}
	if _, err := tx.Exec(ctx, `UPDATE rate_limit_buckets
		SET tokens = $3, updated_at = $4, burst = $5, every_seconds = $6
		WHERE scope = $1 AND key = $2`, s.scope, key, next, now, s.burst, s.every.Seconds()); err != nil {
		return false, 0, fmt.Errorf("write rate limit bucket: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, 0, fmt.Errorf("commit rate limit: %w", err)
	}
	if ok {
		return true, 0, nil
	}
	return false, time.Duration((1 - refilled) * float64(s.every)), nil
}

// Sweep forgets buckets that have filled up again.
func (s *Store) Sweep(ctx context.Context) error {
	_, err := s.db.Exec(ctx, `DELETE FROM rate_limit_buckets
		WHERE scope = $1
		  AND LEAST(burst, tokens + EXTRACT(EPOCH FROM (clock_timestamp() - updated_at)) / every_seconds) >= burst`, s.scope)
	if err != nil {
		return fmt.Errorf("sweep rate limits: %w", err)
	}
	return nil
}
