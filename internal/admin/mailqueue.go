package admin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/pmaxis/helpic/internal/auth"
	"github.com/pmaxis/helpic/internal/mail"
)

// The outgoing email and the sign-ups waiting for theirs, so an admin can
// tell why someone "never got the email" and send it again.

// More of what an admin can do, recorded like the actions on accounts. A
// sign-up has no account yet; the email is the target.
const (
	// ActionRetryEmail sends an email that failed with a fresh set of tries.
	ActionRetryEmail = "retry_email"
	// ActionResendSignup emails a sign-up a new confirmation link.
	ActionResendSignup = "resend_signup"
	// ActionDeleteSignup removes a sign-up nobody confirmed.
	ActionDeleteSignup = "delete_signup"
)

// ErrSignupNotFound is returned for an address with no sign-up waiting.
var ErrSignupNotFound = errors.New("sign-up not found")

// EmailQueue is the outbox as the panel sees it.
type EmailQueue interface {
	List(ctx context.Context, f mail.QueueFilter) ([]mail.Queued, int, error)
	Counts(ctx context.Context) (mail.QueueCounts, error)
	Retry(ctx context.Context, id string) (mail.Queued, error)
}

// SignupLinks emails a sign-up its confirmation link again.
type SignupLinks interface {
	ResendSignup(ctx context.Context, email string) error
}

// UseOutbox lets the panel show the outgoing email and send what failed
// again.
func (s *Service) UseOutbox(q EmailQueue) {
	s.emails = q
}

// UseSignupLinks lets the panel send sign-ups their link again.
func (s *Service) UseSignupLinks(l SignupLinks) {
	s.signupLinks = l
}

// Emails returns a page of the outbox and how many match in all.
func (s *Service) Emails(ctx context.Context, f mail.QueueFilter) ([]mail.Queued, int, error) {
	return s.emails.List(ctx, f)
}

// EmailCounts tells how many emails are waiting, failed and sent.
func (s *Service) EmailCounts(ctx context.Context) (mail.QueueCounts, error) {
	return s.emails.Counts(ctx)
}

// RetryEmail sends the email id again as the admin adminID and records it.
// mail.ErrNotQueued when it is gone or was sent.
func (s *Service) RetryEmail(ctx context.Context, adminID, id string) (mail.Queued, error) {
	q, err := s.emails.Retry(ctx, id)
	if err != nil {
		return mail.Queued{}, err
	}
	return q, record(ctx, s.repo.db, adminID, ActionRetryEmail, q.To, "")
}

// Signup is a public sign-up that hasn't opened its confirmation link.
type Signup struct {
	Email     string
	Name      string
	Locale    string
	CreatedAt time.Time
	ExpiresAt time.Time
	// EmailStatus is how the newest email to the address went, a
	// mail.Status; empty once the outbox no longer keeps it.
	EmailStatus string
	EmailError  string
}

// SignupFilter picks a page of sign-ups, the newest first.
type SignupFilter struct {
	// Query is part of the email or the name.
	Query string
	Page  int
	Limit int
}

// Signups returns a page of the sign-ups still waiting and how many match.
func (s *Service) Signups(ctx context.Context, f SignupFilter) ([]Signup, int, error) {
	return s.repo.Signups(ctx, f)
}

// ResendSignup emails the sign-up for email a new link as the admin
// adminID and records it. ErrSignupNotFound when there is none.
func (s *Service) ResendSignup(ctx context.Context, adminID, email string) error {
	err := s.signupLinks.ResendSignup(ctx, email)
	if errors.Is(err, auth.ErrSignupInvalid) {
		return ErrSignupNotFound
	}
	if err != nil {
		return err
	}
	return record(ctx, s.repo.db, adminID, ActionResendSignup, email, "")
}

// DeleteSignup removes the sign-up for email as the admin adminID, so its
// link stops working, and records it. ErrSignupNotFound when there is none.
func (s *Service) DeleteSignup(ctx context.Context, adminID, email string) error {
	return s.repo.DeleteSignup(ctx, adminID, email)
}

// Signups lists the sign-ups that haven't expired, with how their newest
// email went.
func (r *Repository) Signups(ctx context.Context, f SignupFilter) ([]Signup, int, error) {
	const where = `WHERE p.expires_at > now()
		  AND ($1 = '' OR p.email ILIKE '%' || $1 || '%' OR p.name ILIKE '%' || $1 || '%')`
	q := escapeLike(strings.TrimSpace(f.Query))
	var total int
	if err := r.db.QueryRow(ctx, `SELECT count(*) FROM pending_signups p `+where, q).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count sign-ups: %w", err)
	}
	rows, err := r.db.Query(ctx, `
		SELECT p.email, p.name, p.locale, p.created_at, p.expires_at,
			COALESCE(CASE WHEN e.sent_at IS NOT NULL THEN 'sent' WHEN e.attempts >= $4 THEN 'failed' WHEN e.id IS NOT NULL THEN 'queued' END, ''),
			COALESCE(e.last_error, '')
		FROM pending_signups p
		LEFT JOIN LATERAL (
			SELECT id, sent_at, attempts, last_error FROM emails
			WHERE to_address = p.email
			ORDER BY created_at DESC
			LIMIT 1
		) e ON true
		`+where+`
		ORDER BY p.created_at DESC, p.email
		LIMIT $2 OFFSET $3`, q, f.Limit, (f.Page-1)*f.Limit, mail.MaxAttempts)
	if err != nil {
		return nil, 0, fmt.Errorf("list sign-ups: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Signup, error) {
		var s Signup
		err := row.Scan(&s.Email, &s.Name, &s.Locale, &s.CreatedAt, &s.ExpiresAt, &s.EmailStatus, &s.EmailError)
		return s, err
	})
	if err != nil {
		return nil, 0, fmt.Errorf("scan sign-ups: %w", err)
	}
	return out, total, nil
}

// DeleteSignup removes the sign-up and records it in one transaction.
func (r *Repository) DeleteSignup(ctx context.Context, adminID, email string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	tag, err := tx.Exec(ctx, `DELETE FROM pending_signups WHERE email = $1`, email)
	if err != nil {
		return fmt.Errorf("delete sign-up: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrSignupNotFound
	}
	if err := record(ctx, tx, adminID, ActionDeleteSignup, email, ""); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// execer runs a statement, in a transaction or not.
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// record notes that the admin adminID did action to targetEmail, linking
// the account with that email when there is one.
func record(ctx context.Context, db execer, adminID, action, targetEmail, reason string) error {
	if _, err := db.Exec(ctx, `
		INSERT INTO admin_actions (admin_id, admin_email, action, target_id, target_email, reason)
		SELECT a.id, a.email, $2, (SELECT t.id FROM users t WHERE t.email = $3 AND t.role <> 'admin'), $3, $4
		FROM users a WHERE a.id = $1`,
		adminID, action, targetEmail, reason); err != nil {
		return fmt.Errorf("record admin action: %w", err)
	}
	return nil
}
