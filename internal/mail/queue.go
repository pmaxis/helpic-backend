package mail

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Where a queued email stands.
const (
	// StatusQueued is not sent yet and still being tried.
	StatusQueued = "queued"
	// StatusFailed was given up on after MaxAttempts tries.
	StatusFailed = "failed"
	// StatusSent went out.
	StatusSent = "sent"
)

// ErrNotQueued is returned for an email that is gone or already sent.
var ErrNotQueued = errors.New("email not in the queue")

// Queued is an email in the outbox as the admin panel sees it: whom to,
// about what, and how sending it went. Never the body: it can hold
// one-time links.
type Queued struct {
	ID            string
	To            string
	Subject       string
	Status        string
	Attempts      int
	LastError     string
	NextAttemptAt time.Time
	SentAt        *time.Time
	CreatedAt     time.Time
}

// QueueFilter picks a page of the outbox, the newest first.
type QueueFilter struct {
	// Status is one of the Status constants; empty for all.
	Status string
	// Query is part of the address or the subject.
	Query string
	Page  int
	Limit int
}

// QueueCounts is how many emails stand where, over the week the outbox
// keeps them.
type QueueCounts struct {
	Queued int
	Failed int
	Sent   int
	// LastSent is when an email last went out; nil when none has lately.
	LastSent *time.Time
}

// statusSQL works out an email's Status in SQL.
const statusSQL = `CASE WHEN sent_at IS NOT NULL THEN 'sent' WHEN attempts >= $1 THEN 'failed' ELSE 'queued' END`

// List returns a page of the outbox and how many match in all.
func (o *Outbox) List(ctx context.Context, f QueueFilter) ([]Queued, int, error) {
	const where = `WHERE ($2 = '' OR ` + statusSQL + ` = $2)
		  AND ($3 = '' OR to_address ILIKE '%' || $3 || '%' OR subject ILIKE '%' || $3 || '%')`
	args := []any{MaxAttempts, f.Status, escapeLike(strings.TrimSpace(f.Query))}
	var total int
	if err := o.db.QueryRow(ctx, `SELECT count(*) FROM emails `+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count emails: %w", err)
	}
	rows, err := o.db.Query(ctx, `
		SELECT id, to_address, subject, `+statusSQL+`, attempts, last_error, next_attempt_at, sent_at, created_at
		FROM emails `+where+`
		ORDER BY created_at DESC, id
		LIMIT $4 OFFSET $5`, append(args, f.Limit, (f.Page-1)*f.Limit)...)
	if err != nil {
		return nil, 0, fmt.Errorf("list emails: %w", err)
	}
	out, err := pgx.CollectRows(rows, scanQueued)
	if err != nil {
		return nil, 0, fmt.Errorf("scan emails: %w", err)
	}
	return out, total, nil
}

func scanQueued(row pgx.CollectableRow) (Queued, error) {
	var q Queued
	err := row.Scan(&q.ID, &q.To, &q.Subject, &q.Status, &q.Attempts, &q.LastError, &q.NextAttemptAt, &q.SentAt, &q.CreatedAt)
	return q, err
}

// Counts tells how many emails are waiting, given up on and sent.
func (o *Outbox) Counts(ctx context.Context) (QueueCounts, error) {
	var c QueueCounts
	err := o.db.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE sent_at IS NULL AND attempts < $1),
			count(*) FILTER (WHERE sent_at IS NULL AND attempts >= $1),
			count(*) FILTER (WHERE sent_at IS NOT NULL),
			max(sent_at)
		FROM emails`, MaxAttempts).Scan(&c.Queued, &c.Failed, &c.Sent, &c.LastSent)
	if err != nil {
		return QueueCounts{}, fmt.Errorf("count emails: %w", err)
	}
	return c, nil
}

// Retry sends an email that wasn't sent on the next run, with a fresh set
// of tries, and returns it. ErrNotQueued when it is gone or was sent.
func (o *Outbox) Retry(ctx context.Context, id string) (Queued, error) {
	rows, err := o.db.Query(ctx, `
		UPDATE emails SET attempts = 0, next_attempt_at = now(), failed_at = NULL
		WHERE id::text = $2 AND sent_at IS NULL
		RETURNING id, to_address, subject, `+statusSQL+`, attempts, last_error, next_attempt_at, sent_at, created_at`, MaxAttempts, id)
	if err != nil {
		return Queued{}, fmt.Errorf("retry email: %w", err)
	}
	q, err := pgx.CollectExactlyOneRow(rows, scanQueued)
	if errors.Is(err, pgx.ErrNoRows) {
		return Queued{}, ErrNotQueued
	}
	if err != nil {
		return Queued{}, fmt.Errorf("retry email: %w", err)
	}
	return q, nil
}

// Trouble is what is going wrong with the outgoing email.
type Trouble struct {
	// Retrying is how many emails failed at least RetryingAfter times and
	// are still being tried.
	Retrying int
	// GivenUp are the emails given up on since the time asked about, the
	// oldest first.
	GivenUp []Queued
	// LastError is the newest error of an email still being tried.
	LastError string
}

// RetryingAfter is how many failures make an email count as in trouble: a
// single one is usually a blip.
const RetryingAfter = 3

// Trouble tells how many emails keep failing and which were given up on
// since since.
func (o *Outbox) Trouble(ctx context.Context, since time.Time) (Trouble, error) {
	var t Trouble
	err := o.db.QueryRow(ctx, `
		SELECT count(*),
			COALESCE((SELECT last_error FROM emails WHERE sent_at IS NULL AND attempts >= $2 AND attempts < $1 ORDER BY next_attempt_at DESC LIMIT 1), '')
		FROM emails WHERE sent_at IS NULL AND attempts >= $2 AND attempts < $1`, MaxAttempts, RetryingAfter).Scan(&t.Retrying, &t.LastError)
	if err != nil {
		return Trouble{}, fmt.Errorf("count failing emails: %w", err)
	}
	rows, err := o.db.Query(ctx, `
		SELECT id, to_address, subject, `+statusSQL+`, attempts, last_error, next_attempt_at, sent_at, created_at
		FROM emails WHERE failed_at > $2 AND sent_at IS NULL
		ORDER BY failed_at`, MaxAttempts, since)
	if err != nil {
		return Trouble{}, fmt.Errorf("list given-up emails: %w", err)
	}
	if t.GivenUp, err = pgx.CollectRows(rows, scanQueued); err != nil {
		return Trouble{}, fmt.Errorf("scan given-up emails: %w", err)
	}
	return t, nil
}

// escapeLike makes s match itself in a LIKE pattern.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
