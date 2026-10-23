package admin

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/pmaxis/helpic/internal/i18n"
	"github.com/pmaxis/helpic/internal/mail"
)

// When an admin account is in the wrong hands. From the panel another admin
// can sign it out of a device, and everyone hears of it; nothing there can
// lock an admin out, or a stolen account could lock out the rest. Locking
// and resetting an admin is the server's cli's: whoever runs it has the
// server, which a stolen account doesn't give.

// What the server's cli does to an admin account.
const (
	// ActionLockAdmin stops the admin signing in and signs them out.
	ActionLockAdmin = "lock_admin"
	// ActionUnlockAdmin lets them sign in again.
	ActionUnlockAdmin = "unlock_admin"
	// ActionResetAdmin gives a new password, turns two-factor off, signs
	// them out and lets them in again.
	ActionResetAdmin = "reset_admin"
)

// CLIName is who the action log says did what the cli did.
const CLIName = "server (cli)"

// ErrAdminNotFound is returned for an email that isn't an admin's.
var ErrAdminNotFound = errors.New("admin not found")

// Notifier tells the admins' alert chat at once.
type Notifier interface {
	Tell(ctx context.Context, title, detail string) error
}

// UseAlerts lets the service tell the admins' chat, in locale, when an
// admin account is acted on.
func (s *Service) UseAlerts(n Notifier, locale string) {
	s.alerts, s.alertLocale = n, locale
}

// tell posts to the admins' chat when alerts are on; failing to is logged.
func (s *Service) tell(ctx context.Context, title, detail string) {
	if s.alerts == nil {
		return
	}
	if err := s.alerts.Tell(ctx, title, detail); err != nil {
		slog.Error("failed to send an alert", "error", err)
	}
}

// EndAdminSession signs the admin id out of one device as the admin
// adminID, records it with the reason, emails the admin and tells the
// admins' chat. ErrAdminNotFound for no such admin, ErrSessionNotFound for
// no such session of theirs.
func (s *Service) EndAdminSession(ctx context.Context, adminID, id, sessionID, reason string) error {
	by, target, err := s.repo.EndAdminSession(ctx, adminID, id, sessionID, reason)
	if err != nil {
		return err
	}
	detail := strings.TrimSpace(target.ip + "\n" + reason)
	s.tell(ctx, i18n.T(s.alertLocale, "%s signed the admin %s out of a device", by, target.email), detail)
	if s.mail == nil || by == target.email {
		return nil
	}
	t := func(text string, args ...any) string { return i18n.T(target.locale, text, args...) }
	err = s.mail.Send(ctx, mail.Email{
		To:      target.email,
		Locale:  target.locale,
		Subject: t("Helpic: %s", t("Signed out of a device")),
		Heading: t("Signed out of a device"),
		Paragraphs: []string{
			t("%s signed your admin account, %s, out of one device (%s).", by, target.email, target.ip),
			t("If you don't know why, change your password and set up two-factor sign-in again under Settings → Security."),
		},
	})
	if err != nil {
		slog.Error("failed to queue the signed-out email", "error", err)
	}
	return nil
}

// endedSession is whose session was ended, and where.
type endedSession struct {
	email, locale, ip string
}

// EndAdminSession deletes one session of the admin id and records it, in
// one transaction, and returns who did it and whose it was.
func (r *Repository) EndAdminSession(ctx context.Context, adminID, id, sessionID, reason string) (string, endedSession, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return "", endedSession{}, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var target endedSession
	err = tx.QueryRow(ctx, `SELECT email, locale FROM users WHERE id = $1 AND role = 'admin'`, id).Scan(&target.email, &target.locale)
	if errors.Is(err, pgx.ErrNoRows) || isBadUUID(err) {
		return "", endedSession{}, ErrAdminNotFound
	}
	if err != nil {
		return "", endedSession{}, fmt.Errorf("find admin: %w", err)
	}
	err = tx.QueryRow(ctx, `DELETE FROM sessions WHERE id::text = $1 AND user_id = $2 RETURNING ip`, sessionID, id).Scan(&target.ip)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", endedSession{}, ErrSessionNotFound
	}
	if err != nil {
		return "", endedSession{}, fmt.Errorf("end session: %w", err)
	}
	if err := record(ctx, tx, adminID, ActionEndSession, target.email, reason); err != nil {
		return "", endedSession{}, err
	}
	var by string
	if err := tx.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, adminID).Scan(&by); err != nil {
		return "", endedSession{}, fmt.Errorf("find admin: %w", err)
	}
	return by, target, tx.Commit(ctx)
}

// LockAdmin stops the admin with the email signing in and signs them out
// everywhere, for the cli. ErrAdminNotFound when it isn't an admin's.
func (s *Service) LockAdmin(ctx context.Context, email string) error {
	if err := s.repo.cliAct(ctx, email, ActionLockAdmin, func(tx pgx.Tx, id string) error {
		if _, err := tx.Exec(ctx, `UPDATE users SET blocked_at = now() WHERE id = $1`, id); err != nil {
			return fmt.Errorf("lock admin: %w", err)
		}
		return endAll(ctx, tx, id)
	}); err != nil {
		return err
	}
	s.tell(ctx, i18n.T(s.alertLocale, "The admin %s was locked from the server", email), "")
	return nil
}

// UnlockAdmin lets the admin with the email sign in again, for the cli.
func (s *Service) UnlockAdmin(ctx context.Context, email string) error {
	if err := s.repo.cliAct(ctx, email, ActionUnlockAdmin, func(tx pgx.Tx, id string) error {
		if _, err := tx.Exec(ctx, `UPDATE users SET blocked_at = NULL WHERE id = $1`, id); err != nil {
			return fmt.Errorf("unlock admin: %w", err)
		}
		return nil
	}); err != nil {
		return err
	}
	s.tell(ctx, i18n.T(s.alertLocale, "The admin %s was unlocked from the server", email), "")
	return nil
}

// ResetAdmin gives the admin with the email a new password, which it
// returns, turns their two-factor off, signs them out everywhere and lets
// them sign in again, for the cli: the owner starts over, as after
// create-admin.
func (s *Service) ResetAdmin(ctx context.Context, email string) (string, error) {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate password: %w", err)
	}
	password := base64.RawURLEncoding.EncodeToString(b)
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	if err := s.repo.cliAct(ctx, email, ActionResetAdmin, func(tx pgx.Tx, id string) error {
		if _, err := tx.Exec(ctx, `UPDATE users SET password_hash = $2, blocked_at = NULL,
				two_factor_secret = NULL, two_factor_pending = NULL, two_factor_step = 0
			WHERE id = $1`, id, string(hash)); err != nil {
			return fmt.Errorf("reset admin: %w", err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM two_factor_recovery_codes WHERE user_id = $1`, id); err != nil {
			return fmt.Errorf("delete recovery codes: %w", err)
		}
		return endAll(ctx, tx, id)
	}); err != nil {
		return "", err
	}
	s.tell(ctx, i18n.T(s.alertLocale, "The admin %s was reset from the server: a new password, two-factor off", email), "")
	return password, nil
}

func endAll(ctx context.Context, tx pgx.Tx, id string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, id); err != nil {
		return fmt.Errorf("end sessions: %w", err)
	}
	return nil
}

// cliAct does change to the admin with the email and records it as the
// cli's, in one transaction.
func (r *Repository) cliAct(ctx context.Context, email, action string, change func(tx pgx.Tx, id string) error) error {
	email = strings.ToLower(strings.TrimSpace(email))
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var id string
	err = tx.QueryRow(ctx, `SELECT id FROM users WHERE email = $1 AND role = 'admin' FOR UPDATE`, email).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrAdminNotFound
	}
	if err != nil {
		return fmt.Errorf("find admin: %w", err)
	}
	if err := change(tx, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO admin_actions (admin_email, action, target_email) VALUES ($1, $2, $3)`, CLIName, action, email); err != nil {
		return fmt.Errorf("record admin action: %w", err)
	}
	return tx.Commit(ctx)
}
