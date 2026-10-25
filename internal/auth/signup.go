package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/pmaxis/helpic/internal/i18n"
	"github.com/pmaxis/helpic/internal/mail"
	"github.com/pmaxis/helpic/internal/user"
)

// Public sign-up creates no account until the address opens the link we
// emailed it. Invitations still create an account at once: opening that
// link already proved the inbox.

// signupTTL is how long a sign-up confirmation link works.
const signupTTL = 24 * time.Hour

// ErrSignupInvalid is returned for a confirmation link that is unknown,
// replaced, used or expired.
var ErrSignupInvalid = errors.New("sign-up confirmation link is invalid")

// RequestSignup emails a confirmation link. Nothing is created until it is
// opened. A second request for the same address replaces the first link. An
// address that already has an account gets an email saying so instead, and
// the caller hears the same either way: the answer doesn't tell who has an
// account.
func (s *Service) RequestSignup(ctx context.Context, p RegisterParams) error {
	existing, err := s.repo.GetByEmail(ctx, p.Email)
	if err == nil {
		return s.mail.Send(ctx, accountExistsEmail(existing, s.appURL))
	}
	if !errors.Is(err, user.ErrNotFound) {
		return err
	}

	passwordHash, err := HashPassword(p.Password)
	if err != nil {
		return err
	}
	raw, hash, err := NewToken()
	if err != nil {
		return err
	}
	locale := i18n.FromContext(ctx)
	if err := s.repo.SavePendingSignup(ctx, p.Email, p.Name, passwordHash, locale, hash, time.Now().Add(signupTTL)); err != nil {
		return err
	}

	if err := s.mail.Send(ctx, signupEmail(p.Email, locale, s.appURL+"/confirm-signup/"+raw)); err != nil {
		if delErr := s.repo.DeletePendingSignup(ctx, p.Email, hash); delErr != nil {
			slog.Error("failed to roll back sign-up", "error", delErr)
		}
		return err
	}
	return nil
}

// ResendSignup emails a sign-up that hasn't been confirmed a new link, good
// for another 24 hours, in the language it signed up in. The old link stops
// working. ErrSignupInvalid when the address has no sign-up waiting.
func (s *Service) ResendSignup(ctx context.Context, email string) error {
	raw, hash, err := NewToken()
	if err != nil {
		return err
	}
	locale, err := s.repo.RenewPendingSignup(ctx, email, hash, time.Now().Add(signupTTL))
	if err != nil {
		return err
	}
	return s.mail.Send(ctx, signupEmail(email, locale, s.appURL+"/confirm-signup/"+raw))
}

// signupEmail carries the link that confirms a sign-up.
func signupEmail(email, locale, link string) mail.Email {
	t := func(text string, args ...any) string { return i18n.T(locale, text, args...) }
	return mail.Email{
		To:      email,
		Locale:  locale,
		Subject: t("Confirm your Helpic email"),
		Heading: t("Confirm your email"),
		Paragraphs: []string{
			t("Confirm %s to create your Helpic account.", email),
		},
		Button: &mail.Button{Text: t("Confirm email"), URL: link},
		Note:   t("The link works for 24 hours. If you didn't ask to sign up, ignore this email: no account is created."),
	}
}

// accountExistsEmail answers a sign-up for an address that already has an
// account, in the account's language.
func accountExistsEmail(u user.User, appURL string) mail.Email {
	t := func(text string, args ...any) string { return i18n.T(u.Locale, text, args...) }
	return mail.Email{
		To:      u.Email,
		Locale:  u.Locale,
		Subject: t("You already have a Helpic account"),
		Heading: t("You already have an account"),
		Paragraphs: []string{
			t("Someone asked to create a Helpic account with %s, but this address already has one.", u.Email),
			t("If it was you, sign in. Forgot the password? Reset it from the sign-in page."),
		},
		Button: &mail.Button{Text: t("Sign in"), URL: appURL + "/login"},
		Note:   t("If it wasn't you, ignore this email: nothing changed."),
	}
}

// ConfirmSignup creates the freelancer account the link names and signs
// that browser in. The link is the proof the inbox is theirs.
func (s *Service) ConfirmSignup(ctx context.Context, raw string, c Client) (user.User, Tokens, error) {
	email, name, passwordHash, locale, err := s.repo.TakePendingSignup(ctx, HashToken(raw))
	if err != nil {
		return user.User{}, Tokens{}, err
	}
	u, err := s.repo.CreateUser(ctx, email, name, user.RoleFreelancer, passwordHash, locale)
	if err != nil {
		return user.User{}, Tokens{}, err
	}
	tokens, err := s.StartSession(ctx, u, c)
	if err != nil {
		return user.User{}, Tokens{}, err
	}
	return u, tokens, nil
}

// SavePendingSignup stores a sign-up waiting for its email, replacing an
// earlier one for the same address.
func (r *Repository) SavePendingSignup(ctx context.Context, email, name, passwordHash, locale string, hash []byte, expiresAt time.Time) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO pending_signups (email, name, password_hash, locale, token_hash, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (email) DO UPDATE SET
			name = EXCLUDED.name,
			password_hash = EXCLUDED.password_hash,
			locale = EXCLUDED.locale,
			token_hash = EXCLUDED.token_hash,
			expires_at = EXCLUDED.expires_at,
			created_at = now()`, email, name, passwordHash, locale, hash, expiresAt)
	if err != nil {
		return fmt.Errorf("save sign-up: %w", err)
	}
	return nil
}

// RenewPendingSignup gives the sign-up for email a new link and returns its
// language. ErrSignupInvalid when there is none.
func (r *Repository) RenewPendingSignup(ctx context.Context, email string, hash []byte, expiresAt time.Time) (string, error) {
	var locale string
	err := r.db.QueryRow(ctx, `
		UPDATE pending_signups SET token_hash = $2, expires_at = $3
		WHERE email = $1
		RETURNING locale`, email, hash, expiresAt).Scan(&locale)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrSignupInvalid
	}
	if err != nil {
		return "", fmt.Errorf("renew sign-up: %w", err)
	}
	return locale, nil
}

// TakePendingSignup uses up a confirmation link. ErrSignupInvalid when it
// is unknown, replaced or expired.
func (r *Repository) TakePendingSignup(ctx context.Context, hash []byte) (email, name, passwordHash, locale string, err error) {
	var live bool
	err = r.db.QueryRow(ctx, `
		DELETE FROM pending_signups
		WHERE token_hash = $1
		RETURNING email, name, password_hash, locale, expires_at > now()`, hash).Scan(&email, &name, &passwordHash, &locale, &live)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", "", ErrSignupInvalid
	}
	if err != nil {
		return "", "", "", "", fmt.Errorf("take sign-up: %w", err)
	}
	if !live {
		return "", "", "", "", ErrSignupInvalid
	}
	return email, name, passwordHash, locale, nil
}

// DeletePendingSignup removes the sign-up stored under this link, so a
// failed email doesn't leave a confirmation nobody was sent.
func (r *Repository) DeletePendingSignup(ctx context.Context, email string, hash []byte) error {
	if _, err := r.db.Exec(ctx, `DELETE FROM pending_signups WHERE email = $1 AND token_hash = $2`, email, hash); err != nil {
		return fmt.Errorf("delete sign-up: %w", err)
	}
	return nil
}

// DeleteExpiredSignups removes sign-ups nobody confirmed in time.
func (r *Repository) DeleteExpiredSignups(ctx context.Context) error {
	if _, err := r.db.Exec(ctx, `DELETE FROM pending_signups WHERE expires_at <= now()`); err != nil {
		return fmt.Errorf("delete expired sign-ups: %w", err)
	}
	return nil
}
