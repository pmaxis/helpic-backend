package admin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/pmaxis/helpic/internal/i18n"
	"github.com/pmaxis/helpic/internal/mail"
)

// What an admin can do to an account. Each is recorded when it changes
// something.
const (
	// ActionBlock stops the account signing in and ends its sessions.
	ActionBlock = "block"
	// ActionUnblock lets it sign in again.
	ActionUnblock = "unblock"
	// ActionEndSessions signs it out everywhere.
	ActionEndSessions = "end_sessions"
	// ActionResetTwoFactor turns its two-factor sign-in off and signs it out
	// everywhere, for someone who lost both their phone and recovery codes.
	ActionResetTwoFactor = "reset_two_factor"
)

// MaxReason is how long a reason may be, in characters.
const MaxReason = 500

// Action is one thing an admin did to an account.
type Action struct {
	ID int64
	At time.Time
	// AdminID and TargetID are nil once the account is gone; the emails stay.
	AdminID     *string
	AdminEmail  string
	Action      string
	TargetID    *string
	TargetEmail string
	Reason      string
}

// ActionFilter picks a page of actions, the newest first.
type ActionFilter struct {
	// TargetID keeps the actions on one account; empty for all.
	TargetID string
	Page     int
	Limit    int
}

// Mailer queues an email.
type Mailer interface {
	Send(ctx context.Context, e mail.Email) error
}

// UseMail lets the service tell people by email when an admin changed how
// they sign in.
func (s *Service) UseMail(m Mailer) {
	s.mail = m
}

// Act does action to the account id as the admin adminID, records it with
// the reason when it changed something, and returns the account as it is
// now. ErrNotFound for no such account, a deleted one, or an admin's.
func (s *Service) Act(ctx context.Context, adminID, id, action, reason string) (AccountDetail, error) {
	changed, err := s.repo.Act(ctx, adminID, id, action, reason)
	if err != nil {
		return AccountDetail{}, err
	}
	d, err := s.AccountDetail(ctx, id)
	if err != nil {
		return AccountDetail{}, err
	}
	if changed && action == ActionResetTwoFactor {
		s.twoFactorResetNotice(ctx, d)
	}
	return d, nil
}

// twoFactorResetNotice tells the person their two-factor sign-in was turned
// off, in case they didn't ask for it.
func (s *Service) twoFactorResetNotice(ctx context.Context, d AccountDetail) {
	if s.mail == nil {
		return
	}
	heading := "Two-factor sign-in was reset"
	err := s.mail.Send(ctx, mail.Email{
		To:      d.Email,
		Locale:  d.Locale,
		Subject: i18n.T(d.Locale, "Helpic: %s", i18n.T(d.Locale, heading)),
		Heading: i18n.T(d.Locale, heading),
		Paragraphs: []string{
			i18n.T(d.Locale, "Helpic's support turned off two-factor sign-in for your account, %s, and signed it out on every device. Signing in takes only the password now; turn two-factor on again under Settings → Security.", d.Email),
			i18n.T(d.Locale, "If you didn't ask for this, change your password right away and write to Helpic's support."),
		},
	})
	if err != nil {
		slog.Error("failed to queue two-factor reset email", "error", err)
	}
}

// Actions returns a page of actions and how many match in all.
func (s *Service) Actions(ctx context.Context, f ActionFilter) ([]Action, int, error) {
	return s.repo.Actions(ctx, f)
}

// Act changes the account in one transaction and records the change.
func (r *Repository) Act(ctx context.Context, adminID, id, action, reason string) (bool, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var target string
	err = tx.QueryRow(ctx, `SELECT email FROM users WHERE id = $1 AND role <> 'admin' AND deleted_at IS NULL FOR UPDATE`, id).Scan(&target)
	if errors.Is(err, pgx.ErrNoRows) || isBadUUID(err) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, fmt.Errorf("find account: %w", err)
	}

	changed, err := change(ctx, tx, id, action)
	if err != nil {
		return false, err
	}
	if !changed {
		return false, nil
	}
	if err := record(ctx, tx, adminID, action, target, reason); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

// change does action to the account and reports whether anything changed.
func change(ctx context.Context, tx pgx.Tx, id, action string) (bool, error) {
	exec := func(sql string) (int64, error) {
		tag, err := tx.Exec(ctx, sql, id)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", action, err)
		}
		return tag.RowsAffected(), nil
	}
	endSessions := func() (int64, error) { return exec(`DELETE FROM sessions WHERE user_id = $1`) }

	switch action {
	case ActionBlock:
		n, err := exec(`UPDATE users SET blocked_at = now() WHERE id = $1 AND blocked_at IS NULL`)
		if err != nil || n == 0 {
			return false, err
		}
		_, err = endSessions()
		return true, err
	case ActionUnblock:
		n, err := exec(`UPDATE users SET blocked_at = NULL WHERE id = $1 AND blocked_at IS NOT NULL`)
		return n > 0, err
	case ActionEndSessions:
		n, err := endSessions()
		return n > 0, err
	case ActionResetTwoFactor:
		n, err := exec(`UPDATE users SET two_factor_secret = NULL, two_factor_pending = NULL, two_factor_step = 0
			WHERE id = $1 AND two_factor_secret IS NOT NULL`)
		if err != nil || n == 0 {
			return false, err
		}
		if _, err := exec(`DELETE FROM two_factor_recovery_codes WHERE user_id = $1`); err != nil {
			return false, err
		}
		_, err = endSessions()
		return true, err
	}
	return false, fmt.Errorf("unknown admin action %q", action)
}

// isBadUUID tells whether err is PostgreSQL refusing a malformed UUID.
func isBadUUID(err error) bool {
	var pgErr interface{ SQLState() string }
	return errors.As(err, &pgErr) && pgErr.SQLState() == "22P02"
}

// Actions returns a page of actions, the newest first, and how many match.
func (r *Repository) Actions(ctx context.Context, f ActionFilter) ([]Action, int, error) {
	const where = `WHERE ($1 = '' OR target_id = as_uuid($1))`
	var total int
	if err := r.db.QueryRow(ctx, `SELECT count(*) FROM admin_actions `+where, f.TargetID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count admin actions: %w", err)
	}
	rows, err := r.db.Query(ctx, `SELECT id, at, admin_id, admin_email, action, target_id, target_email, reason
		FROM admin_actions `+where+`
		ORDER BY id DESC
		LIMIT $2 OFFSET $3`, f.TargetID, f.Limit, (f.Page-1)*f.Limit)
	if err != nil {
		return nil, 0, fmt.Errorf("list admin actions: %w", err)
	}
	defer rows.Close()
	out := []Action{}
	for rows.Next() {
		var a Action
		if err := rows.Scan(&a.ID, &a.At, &a.AdminID, &a.AdminEmail, &a.Action, &a.TargetID, &a.TargetEmail, &a.Reason); err != nil {
			return nil, 0, fmt.Errorf("scan admin action: %w", err)
		}
		out = append(out, a)
	}
	return out, total, rows.Err()
}
