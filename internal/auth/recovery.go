package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/pmaxis/helpic/internal/mail"
	"github.com/pmaxis/helpic/internal/user"
)

// Forgotten passwords: a one-time link by email sets a new one. Asking never
// tells whether the email has an account.

// resetTTL is how long a reset link works.
const resetTTL = time.Hour

// ErrResetInvalid is returned for a reset link that is unknown, used or
// expired.
var ErrResetInvalid = errors.New("password reset link is invalid")

// Mailer queues email. Implemented by *mail.Outbox.
type Mailer interface {
	Send(ctx context.Context, e mail.Email) error
}

// PasswordChangedEmail tells someone their password changed, in case it
// wasn't them.
func PasswordChangedEmail(to string) mail.Email {
	return mail.Email{
		To:      to,
		Subject: "Your Helpic password was changed",
		Heading: "Your password was changed",
		Paragraphs: []string{
			"The password for your Helpic account, " + to + ", was just changed, and your other devices were signed out.",
			"If that was you, there's nothing to do. If it wasn't, reset your password right away from the sign-in page, and check which devices are signed in under Settings → Sessions.",
		},
	}
}

// RequestReset emails a reset link to the account with this email, if there
// is one; for an unknown email it does nothing and says nothing.
func (s *Service) RequestReset(ctx context.Context, email string) error {
	u, err := s.repo.GetByEmail(ctx, email)
	if errors.Is(err, user.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	raw, hash, err := newRefreshToken()
	if err != nil {
		return err
	}
	if err := s.repo.CreateReset(ctx, u.ID, hash, time.Now().Add(resetTTL)); err != nil {
		return err
	}
	return s.mail.Send(ctx, mail.Email{
		To:      u.Email,
		Subject: "Reset your Helpic password",
		Heading: "Reset your password",
		Paragraphs: []string{
			"Someone asked to reset the password for your Helpic account, " + u.Email + ". If it was you, set a new one below.",
		},
		Button: &mail.Button{Text: "Set a new password", URL: s.appURL + "/reset-password/" + raw},
		Note:   "The link works once, for an hour. If you didn't ask for it, ignore this email: your password stays as it is.",
	})
}

// ResetPassword sets a new password with a reset link, signs out every
// device and says so by email.
func (s *Service) ResetPassword(ctx context.Context, raw, password string) error {
	userID, email, err := s.repo.TakeReset(ctx, hashToken(raw))
	if err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	if err := s.repo.SetPasswordAndSignOut(ctx, userID, string(hash)); err != nil {
		return err
	}
	if err := s.mail.Send(ctx, PasswordChangedEmail(email)); err != nil {
		slog.Error("failed to queue password changed email", "error", err)
	}
	return nil
}

// --- repository ----------------------------------------------------------------

// CreateReset stores a reset link for the user, replacing earlier ones.
func (r *Repository) CreateReset(ctx context.Context, userID string, hash []byte, expiresAt time.Time) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `DELETE FROM password_resets WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("clear password resets: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO password_resets (token_hash, user_id, expires_at) VALUES ($1, $2, $3)`,
		hash, userID, expiresAt); err != nil {
		return fmt.Errorf("create password reset: %w", err)
	}
	return tx.Commit(ctx)
}

// TakeReset uses up a reset link and returns its user and their email.
// ErrResetInvalid when it is unknown or expired.
func (r *Repository) TakeReset(ctx context.Context, hash []byte) (userID, email string, err error) {
	var live bool
	err = r.db.QueryRow(ctx, `
		DELETE FROM password_resets p USING users u
		WHERE p.token_hash = $1 AND u.id = p.user_id
		RETURNING p.user_id, u.email, p.expires_at > now()`, hash).Scan(&userID, &email, &live)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrResetInvalid
	}
	if err != nil {
		return "", "", fmt.Errorf("take password reset: %w", err)
	}
	if !live {
		return "", "", ErrResetInvalid
	}
	return userID, email, nil
}

// SetPasswordAndSignOut sets the user's password and ends all their sessions.
func (r *Repository) SetPasswordAndSignOut(ctx context.Context, userID, passwordHash string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `UPDATE users SET password_hash = $2 WHERE id = $1`, userID, passwordHash); err != nil {
		return fmt.Errorf("set password: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("end sessions: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM password_resets WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("clear password resets: %w", err)
	}
	return tx.Commit(ctx)
}

// DeleteExpiredResets removes reset links nobody used in time.
func (r *Repository) DeleteExpiredResets(ctx context.Context) error {
	if _, err := r.db.Exec(ctx, `DELETE FROM password_resets WHERE expires_at <= now()`); err != nil {
		return fmt.Errorf("delete expired password resets: %w", err)
	}
	return nil
}
