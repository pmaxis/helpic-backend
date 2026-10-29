// Package account deletes accounts: the person's own choice in the settings,
// or an admin's for them. The users row stays, so the work it shares with
// others keeps its author, but nothing in it says who that was any more.
package account

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/pmaxis/helpic/internal/i18n"
	"github.com/pmaxis/helpic/internal/mail"
)

var (
	// ErrNotFound is returned for no such account, one deleted already, or an
	// admin's: admins aren't deleted this way.
	ErrNotFound = errors.New("account not found")
	// ErrHasTeam is returned for a client whose company still has people:
	// they would lose the company's work without a word.
	ErrHasTeam = errors.New("the company still has people")
)

// DeletedName is what a deleted account is called, in its own language: the
// others see it beside the work it left.
const DeletedName = "Deleted user"

// Deleted is who an account was, for the email that says it is gone.
type Deleted struct {
	ID     string
	Email  string
	Locale string
}

// Mailer queues an email. Implemented by *mail.Outbox.
type Mailer interface {
	Send(ctx context.Context, e mail.Email) error
}

// Files removes uploaded files. Implemented by *attachments.Store.
type Files interface {
	Remove(id string) error
}

// Service deletes accounts.
type Service struct {
	db    *pgxpool.Pool
	mail  Mailer
	files Files
}

// NewService creates an account Service; files removes what the account's
// personal tasks had attached.
func NewService(db *pgxpool.Pool, mail Mailer, files Files) *Service {
	return &Service{db: db, mail: mail, files: files}
}

// Deletable checks what Delete would refuse, before anything is asked that
// can't be asked twice, like a two-factor code: ErrNotFound or ErrHasTeam.
func (s *Service) Deletable(ctx context.Context, userID string) error {
	var found, team bool
	err := s.db.QueryRow(ctx, `SELECT true, EXISTS (
			SELECT 1 FROM companies c JOIN company_members m ON m.company_id = c.id WHERE c.owner_id = u.id)
		FROM users u WHERE u.id = as_uuid($1) AND u.role <> 'admin' AND u.deleted_at IS NULL`, userID).Scan(&found, &team)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ErrNotFound
	case err != nil:
		return fmt.Errorf("check account: %w", err)
	case team:
		return ErrHasTeam
	}
	return nil
}

// Before runs in the deleting transaction while the account is still as it
// was, e.g. to record who deleted it.
type Before func(ctx context.Context, tx pgx.Tx, d Deleted) error

// Delete deletes the account and emails its old address that it's gone;
// bySupport says an admin did it. before, when not nil, runs first in the
// same transaction.
//
// What stays is what others share: tasks and projects with a client, and the
// comments, files, time and history on them; reports; the hourly rates their
// prices come from. What goes is the person's own: personal tasks, projects,
// reports, labels and statuses; their notifications, sessions, devices,
// Telegram, settings and pending invitations; their place in a client's
// company. A running timer stops.
func (s *Service) Delete(ctx context.Context, userID string, bySupport bool, before Before) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var d Deleted
	err = tx.QueryRow(ctx, `SELECT id, email, locale FROM users
		WHERE id = as_uuid($1) AND role <> 'admin' AND deleted_at IS NULL FOR UPDATE`, userID).Scan(&d.ID, &d.Email, &d.Locale)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("find account: %w", err)
	}
	var team bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM companies c JOIN company_members m ON m.company_id = c.id WHERE c.owner_id = $1)`, d.ID).Scan(&team); err != nil {
		return fmt.Errorf("check team: %w", err)
	}
	if team {
		return ErrHasTeam
	}
	if before != nil {
		if err := before(ctx, tx, d); err != nil {
			return err
		}
	}

	// Files of the personal tasks, removed from disk once the rows are gone.
	rows, err := tx.Query(ctx, `SELECT a.id FROM attachments a JOIN tasks t ON t.id = a.task_id
		WHERE t.freelancer_id = $1 AND t.client_id IS NULL`, d.ID)
	if err != nil {
		return fmt.Errorf("list personal files: %w", err)
	}
	files, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return fmt.Errorf("scan personal files: %w", err)
	}

	for _, q := range []string{
		// Their own work: personal tasks first, as statuses can't go while a task has one.
		`DELETE FROM tasks WHERE freelancer_id = $1 AND client_id IS NULL`,
		`DELETE FROM projects WHERE freelancer_id = $1 AND client_id IS NULL`,
		`DELETE FROM reports WHERE freelancer_id = $1 AND client_id IS NULL`,
		`DELETE FROM labels WHERE freelancer_id = $1 AND client_id IS NULL`,
		`DELETE FROM workflow_statuses ws WHERE ws.freelancer_id = $1 AND ws.client_id IS NULL AND ws.project_id IS NULL
			AND NOT EXISTS (SELECT 1 FROM tasks t WHERE t.status_id = ws.id)`,
		// A timer left running would keep counting.
		`UPDATE time_entries
			SET seconds = seconds + greatest(0, extract(epoch FROM now() - running_since))::int, running_since = NULL
			WHERE user_id = $1 AND running_since IS NOT NULL`,
		// Their place in a company, and a company of their own with no one left in it.
		`DELETE FROM company_members WHERE user_id = $1`,
		`DELETE FROM companies WHERE owner_id = $1`,
		`DELETE FROM invitations WHERE freelancer_id = $1 AND accepted_at IS NULL`,
		`DELETE FROM freelancer_invitations WHERE client_id = $1 AND accepted_at IS NULL`,
		`DELETE FROM notifications WHERE user_id = $1`,
		`DELETE FROM sessions WHERE user_id = $1`,
		`DELETE FROM known_devices WHERE user_id = $1`,
		`DELETE FROM password_resets WHERE user_id = $1`,
		`DELETE FROM email_changes WHERE user_id = $1`,
		`DELETE FROM two_factor_recovery_codes WHERE user_id = $1`,
		`DELETE FROM telegram_links WHERE user_id = $1`,
		`DELETE FROM telegram_link_codes WHERE user_id = $1`,
		`DELETE FROM telegram_messages WHERE user_id = $1`,
		`DELETE FROM telegram_drafts WHERE user_id = $1`,
		`DELETE FROM email_notification_prefs WHERE user_id = $1`,
		`DELETE FROM app_notification_prefs WHERE user_id = $1`,
		`DELETE FROM user_activity WHERE user_id = $1`,
	} {
		if _, err := tx.Exec(ctx, q, d.ID); err != nil {
			return fmt.Errorf("delete account data: %w", err)
		}
	}
	// Mail still waiting for them would go to an address that's no longer theirs here.
	if _, err := tx.Exec(ctx, `DELETE FROM emails WHERE to_address = $1 AND sent_at IS NULL`, d.Email); err != nil {
		return fmt.Errorf("delete queued email: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET deleted_at = now(), email = '', first_name = $2, last_name = '', password_hash = '',
			two_factor_secret = NULL, two_factor_pending = NULL, two_factor_step = 0
		WHERE id = $1`, d.ID, i18n.T(d.Locale, DeletedName)); err != nil {
		return fmt.Errorf("delete account: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	for _, id := range files {
		if err := s.files.Remove(id); err != nil {
			slog.Error("remove file of a deleted account", "attachment_id", id, "error", err)
		}
	}
	s.notice(ctx, d, bySupport)
	return nil
}

// notice tells the old address the account is gone, in case it wasn't them.
func (s *Service) notice(ctx context.Context, d Deleted, bySupport bool) {
	t := func(text string, args ...any) string { return i18n.T(d.Locale, text, args...) }
	first := t("Your Helpic account, %s, was deleted and signed out on every device.", d.Email)
	if bySupport {
		first = t("Helpic's support deleted your Helpic account, %s, and signed it out on every device.", d.Email)
	}
	err := s.mail.Send(ctx, mail.Email{
		To:      d.Email,
		Locale:  d.Locale,
		Subject: t("Your Helpic account was deleted"),
		Heading: t("Your account was deleted"),
		Paragraphs: []string{
			first,
			t("The work you shared with others stays with them, under “%s”. Everything else of yours is gone. You can sign up again with this email at any time.", t(DeletedName)),
		},
	})
	if err != nil {
		slog.Error("failed to queue account deleted email", "error", err)
	}
}
