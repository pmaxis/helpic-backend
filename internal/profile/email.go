package profile

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/pmaxis/helpic/internal/auth"
	"github.com/pmaxis/helpic/internal/i18n"
	"github.com/pmaxis/helpic/internal/mail"
	"github.com/pmaxis/helpic/internal/user"
)

// A new sign-in email takes effect only once it is confirmed from its own
// inbox, so a typo or someone else's address can't take the account. The old
// address hears about the change.

// emailChangeTTL is how long a confirmation link works.
const emailChangeTTL = 24 * time.Hour

var (
	// ErrSameEmail is returned when the new email is the current one.
	ErrSameEmail = errors.New("that is the current email")

	// ErrEmailChangeInvalid is returned for a confirmation link that is
	// unknown, used or expired.
	ErrEmailChangeInvalid = errors.New("email confirmation link is invalid")
)

// RequestEmailChange checks the current password and that the email is free,
// and emails a confirmation link to the new address. The email stays as it is
// until the link is opened.
func (s *Service) RequestEmailChange(ctx context.Context, userID, email, currentPassword string) error {
	u, err := s.verify(ctx, userID, currentPassword)
	if err != nil {
		return err
	}
	if u.Email == email {
		return ErrSameEmail
	}
	taken, err := s.repo.EmailTaken(ctx, email)
	if err != nil {
		return err
	}
	if taken {
		return auth.ErrEmailExists
	}

	raw, hash, err := auth.NewToken()
	if err != nil {
		return err
	}
	if err := s.repo.CreateEmailChange(ctx, userID, hash, email, time.Now().Add(emailChangeTTL)); err != nil {
		return err
	}
	t := func(text string, args ...any) string { return i18n.T(u.Locale, text, args...) }
	return s.mail.Send(ctx, mail.Email{
		To:      email,
		Locale:  u.Locale,
		Subject: t("Confirm your new Helpic email"),
		Heading: t("Confirm your new email"),
		Paragraphs: []string{
			t("You asked to sign in to Helpic with %s instead of %s. Confirm it's yours, and it becomes your sign-in email.", email, u.Email),
		},
		Button: &mail.Button{Text: t("Confirm email"), URL: s.appURL + "/confirm-email/" + raw},
		Note:   t("The link works for 24 hours. If you didn't ask for this, ignore this email: nothing changes."),
	})
}

// ConfirmEmailChange makes the confirmed email the account's sign-in email
// and tells the old address. It needs no signed-in user: the link is the
// proof.
func (s *Service) ConfirmEmailChange(ctx context.Context, raw string) (user.User, error) {
	userID, email, err := s.repo.TakeEmailChange(ctx, auth.HashToken(raw))
	if err != nil {
		return user.User{}, err
	}
	before, err := s.repo.Get(ctx, userID)
	if err != nil {
		return user.User{}, err
	}
	u, err := s.repo.SetEmail(ctx, userID, email)
	if err != nil {
		return user.User{}, err
	}
	t := func(text string, args ...any) string { return i18n.T(u.Locale, text, args...) }
	if err := s.mail.Send(ctx, mail.Email{
		To:      before.Email,
		Locale:  u.Locale,
		Subject: t("Your Helpic sign-in email was changed"),
		Heading: t("Your sign-in email was changed"),
		Paragraphs: []string{
			t("Your Helpic account now signs in with %s instead of this address, %s.", u.Email, before.Email),
			t("If that was you, there's nothing to do. If it wasn't, someone else may have your password."),
		},
	}); err != nil {
		slog.Error("failed to queue email changed notice", "error", err)
	}
	return u, nil
}

// Cleanup removes confirmation links nobody opened in time.
func (s *Service) Cleanup(ctx context.Context) error {
	return s.repo.DeleteExpiredEmailChanges(ctx)
}

// --- repository ----------------------------------------------------------------

// EmailTaken reports whether an account signs in with this email.
func (r *Repository) EmailTaken(ctx context.Context, email string) (bool, error) {
	var taken bool
	if err := r.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE email = $1)`, email).Scan(&taken); err != nil {
		return false, fmt.Errorf("check email: %w", err)
	}
	return taken, nil
}

// CreateEmailChange stores a pending email change, replacing an earlier one.
func (r *Repository) CreateEmailChange(ctx context.Context, userID string, hash []byte, email string, expiresAt time.Time) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `DELETE FROM email_changes WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("clear email changes: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO email_changes (token_hash, user_id, new_email, expires_at) VALUES ($1, $2, $3, $4)`,
		hash, userID, email, expiresAt); err != nil {
		return fmt.Errorf("create email change: %w", err)
	}
	return tx.Commit(ctx)
}

// TakeEmailChange uses up a confirmation link and returns its user and new
// email. ErrEmailChangeInvalid when it is unknown or expired.
func (r *Repository) TakeEmailChange(ctx context.Context, hash []byte) (userID, email string, err error) {
	var live bool
	err = r.db.QueryRow(ctx, `
		DELETE FROM email_changes WHERE token_hash = $1
		RETURNING user_id, new_email, expires_at > now()`, hash).Scan(&userID, &email, &live)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrEmailChangeInvalid
	}
	if err != nil {
		return "", "", fmt.Errorf("take email change: %w", err)
	}
	if !live {
		return "", "", ErrEmailChangeInvalid
	}
	return userID, email, nil
}

// DeleteExpiredEmailChanges removes confirmation links nobody opened in time.
func (r *Repository) DeleteExpiredEmailChanges(ctx context.Context) error {
	if _, err := r.db.Exec(ctx, `DELETE FROM email_changes WHERE expires_at <= now()`); err != nil {
		return fmt.Errorf("delete expired email changes: %w", err)
	}
	return nil
}
