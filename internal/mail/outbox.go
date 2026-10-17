package mail

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// batchSize is how many emails one run sends at most.
	batchSize = 20
	// maxAttempts is how many times a send is tried before giving up.
	maxAttempts = 8
	// firstRetry doubles after each failure, up to maxRetry.
	firstRetry = time.Minute
	maxRetry   = 6 * time.Hour
	// keepFor is how long sent and abandoned emails are kept.
	keepFor = 7 * 24 * time.Hour
)

// Outbox stores outgoing email and sends it in the background.
type Outbox struct {
	db     *pgxpool.Pool
	sender Sender
}

// NewOutbox creates an Outbox that sends through sender.
func NewOutbox(db *pgxpool.Pool, sender Sender) *Outbox {
	return &Outbox{db: db, sender: sender}
}

// Send renders the email and stores it to be sent. It returns once it is
// stored; Run delivers it.
func (o *Outbox) Send(ctx context.Context, e Email) error {
	m, err := Render(e)
	if err != nil {
		return err
	}
	if _, err := o.db.Exec(ctx, `INSERT INTO emails (to_address, subject, html, text) VALUES ($1, $2, $3, $4)`,
		m.To, m.Subject, m.HTML, m.Text); err != nil {
		return fmt.Errorf("queue email: %w", err)
	}
	return nil
}

// Run sends the emails that are due. Rows are locked while they're sent, so
// several servers never send the same one. A failure is tried again later,
// a little later each time.
func (o *Outbox) Run(ctx context.Context) error {
	tx, err := o.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	rows, err := tx.Query(ctx, `
		SELECT id, to_address, subject, html, text, attempts FROM emails
		WHERE sent_at IS NULL AND attempts < $1 AND next_attempt_at <= now()
		ORDER BY created_at
		LIMIT $2
		FOR UPDATE SKIP LOCKED`, maxAttempts, batchSize)
	if err != nil {
		return fmt.Errorf("load emails: %w", err)
	}
	type queued struct {
		id       string
		m        Message
		attempts int
	}
	due, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (queued, error) {
		var q queued
		err := row.Scan(&q.id, &q.m.To, &q.m.Subject, &q.m.HTML, &q.m.Text, &q.attempts)
		return q, err
	})
	if err != nil {
		return fmt.Errorf("scan emails: %w", err)
	}

	for _, q := range due {
		if sendErr := o.sender.Send(ctx, q.m); sendErr != nil {
			attempts := q.attempts + 1
			if attempts >= maxAttempts {
				slog.Error("email abandoned", "id", q.id, "attempts", attempts, "error", sendErr)
			}
			if _, err := tx.Exec(ctx, `UPDATE emails SET attempts = $2, last_error = $3, next_attempt_at = now() + make_interval(secs => $4) WHERE id = $1`,
				q.id, attempts, sendErr.Error(), retryAfter(attempts).Seconds()); err != nil {
				return fmt.Errorf("record failed email: %w", err)
			}
			continue
		}
		// The bodies can hold one-time links: don't keep them.
		if _, err := tx.Exec(ctx, `UPDATE emails SET sent_at = now(), attempts = attempts + 1, html = '', text = '' WHERE id = $1`, q.id); err != nil {
			return fmt.Errorf("record sent email: %w", err)
		}
	}
	return tx.Commit(ctx)
}

// retryAfter is the wait after the nth failure.
func retryAfter(attempts int) time.Duration {
	d := firstRetry << (attempts - 1)
	if d <= 0 || d > maxRetry {
		return maxRetry
	}
	return d
}

// Cleanup removes emails sent, or given up on, long enough ago.
func (o *Outbox) Cleanup(ctx context.Context) error {
	if _, err := o.db.Exec(ctx, `
		DELETE FROM emails
		WHERE (sent_at IS NOT NULL OR attempts >= $1) AND created_at < $2`, maxAttempts, time.Now().Add(-keepFor)); err != nil {
		return fmt.Errorf("delete old emails: %w", err)
	}
	return nil
}
