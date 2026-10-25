package notifications

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/pmaxis/helpic/internal/i18n"
	"github.com/pmaxis/helpic/internal/mail"
)

// Notifications by email. A notification waits emailDelay first, so what
// happens together (a task added, then commented on) arrives as one email;
// one already read in the app isn't emailed at all. Users pick the kinds,
// like for Telegram; the defaults are the important ones.

const (
	// emailDelay is how long a notification waits before it's emailed.
	emailDelay = 5 * time.Minute
	// emailUsersPerRun bounds how many users one run emails.
	emailUsersPerRun = 50
	// emailListed is how many notifications one email lists at most.
	emailListed = 20
)

// ErrUnknownKind is returned for a notification kind that doesn't exist.
var ErrUnknownKind = errors.New("unknown notification kind")

// Mailer queues email. Implemented by *mail.Outbox.
type Mailer interface {
	Send(ctx context.Context, e mail.Email) error
}

// Emailer sends notifications by email and keeps each user's choice of kinds.
type Emailer struct {
	db   *pgxpool.Pool
	mail Mailer
	// appURL is the frontend the emails link to.
	appURL string
}

// NewEmailer creates an Emailer.
func NewEmailer(db *pgxpool.Pool, mail Mailer, appURL string) *Emailer {
	return &Emailer{db: db, mail: mail, appURL: strings.TrimRight(appURL, "/")}
}

// Kinds returns the kinds the user gets by email.
func (e *Emailer) Kinds(ctx context.Context, userID string) ([]string, error) {
	var kinds []string
	err := e.db.QueryRow(ctx, `SELECT kinds FROM email_notification_prefs WHERE user_id = $1`, userID).Scan(&kinds)
	if errors.Is(err, pgx.ErrNoRows) {
		return slices.Clone(DefaultKinds), nil
	}
	if err != nil {
		return nil, fmt.Errorf("get email kinds: %w", err)
	}
	return kinds, nil
}

// SetKinds chooses which kinds the user gets by email; none turns email off.
func (e *Emailer) SetKinds(ctx context.Context, userID string, kinds []string) ([]string, error) {
	clean, err := cleanKinds(kinds)
	if err != nil {
		return nil, err
	}
	if _, err := e.db.Exec(ctx, `
		INSERT INTO email_notification_prefs (user_id, kinds) VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET kinds = EXCLUDED.kinds, updated_at = now()`, userID, clean); err != nil {
		return nil, fmt.Errorf("set email kinds: %w", err)
	}
	return clean, nil
}

// Run emails everyone whose oldest waiting notification has waited long
// enough: all their waiting notifications in one email. Run it every minute.
func (e *Emailer) Run(ctx context.Context) error {
	rows, err := e.db.Query(ctx, `
		SELECT user_id FROM notifications
		WHERE emailed_at IS NULL AND created_at <= $1
		GROUP BY user_id
		LIMIT $2`, time.Now().Add(-emailDelay), emailUsersPerRun)
	if err != nil {
		return fmt.Errorf("find notifications to email: %w", err)
	}
	users, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return fmt.Errorf("scan users to email: %w", err)
	}
	var errs []error
	for _, userID := range users {
		errs = append(errs, e.digest(ctx, userID))
	}
	return errors.Join(errs...)
}

// digest deals with the user's waiting notifications: the unread ones of the
// kinds they chose go in one email, and all are marked dealt with.
func (e *Emailer) digest(ctx context.Context, userID string) error {
	tx, err := e.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Locked, so two servers never email the same ones.
	rows, err := tx.Query(ctx, `
		SELECT n.id, n.kind, coalesce(n.task_id::text, ''), coalesce(n.project_id::text, ''), coalesce(n.report_id::text, ''),
			n.subject, n.detail, n.read_at IS NOT NULL, coalesce(a.name, ''), coalesce(a.email, '')
		FROM notifications n
		LEFT JOIN users a ON a.id = n.actor_id
		WHERE n.user_id = $1 AND n.emailed_at IS NULL
		ORDER BY n.created_at
		FOR UPDATE OF n SKIP LOCKED`, userID)
	if err != nil {
		return fmt.Errorf("load notifications to email: %w", err)
	}
	type waiting struct {
		n    Notification
		read bool
	}
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (waiting, error) {
		var w waiting
		err := row.Scan(&w.n.ID, &w.n.Kind, &w.n.TaskID, &w.n.ProjectID, &w.n.ReportID,
			&w.n.Subject, &w.n.Detail, &w.read, &w.n.ActorName, &w.n.ActorEmail)
		return w, err
	})
	if err != nil {
		return fmt.Errorf("scan notifications to email: %w", err)
	}
	if len(list) == 0 {
		return nil
	}

	kinds, err := e.Kinds(ctx, userID)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(list))
	var send []Notification
	for _, w := range list {
		ids = append(ids, w.n.ID)
		if !w.read && slices.Contains(kinds, w.n.Kind) {
			send = append(send, w.n)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE notifications SET emailed_at = now() WHERE id = ANY($1::uuid[])`, ids); err != nil {
		return fmt.Errorf("mark notifications emailed: %w", err)
	}
	if len(send) > 0 {
		var to, locale string
		if err := tx.QueryRow(ctx, `SELECT email, locale FROM users WHERE id = $1`, userID).Scan(&to, &locale); err != nil {
			return fmt.Errorf("get recipient: %w", err)
		}
		if err := e.mail.Send(ctx, e.compose(to, locale, send)); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// compose words the email in the recipient's language: one notification is
// its own email, several are listed under "What's new".
func (e *Emailer) compose(to, locale string, list []Notification) mail.Email {
	t := func(text string, args ...any) string { return i18n.T(locale, text, args...) }
	note := t("You get these emails for your Helpic notifications. Choose which in Settings → Notifications.")
	if len(list) == 1 {
		n := list[0]
		line := Describe(n, locale)
		var paras []string
		if n.Kind == KindCommentAdded && n.Detail != "" {
			paras = append(paras, "“"+n.Detail+"”")
		}
		return mail.Email{
			To:         to,
			Locale:     locale,
			Subject:    oneLine(line),
			Heading:    line,
			Paragraphs: paras,
			Button:     &mail.Button{Text: t("Open in Helpic"), URL: e.appURL + Path(n)},
			Note:       note,
		}
	}

	paras := make([]string, 0, min(len(list), emailListed)+1)
	for _, n := range list[:min(len(list), emailListed)] {
		line := "• " + Describe(n, locale)
		if n.Kind == KindCommentAdded && n.Detail != "" {
			line += ": “" + n.Detail + "”"
		}
		paras = append(paras, line)
	}
	if more := len(list) - emailListed; more > 0 {
		paras = append(paras, t("…and %d more in the app.", more))
	}
	return mail.Email{
		To:         to,
		Locale:     locale,
		Subject:    t("%s on Helpic", i18n.Count(locale, len(list), "update")),
		Heading:    t("What's new"),
		Paragraphs: paras,
		Button:     &mail.Button{Text: t("Open Helpic"), URL: e.appURL + "/dashboard"},
		Note:       note,
	}
}

// oneLine fits text into a subject line: no line breaks, not too long.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 140 {
		s = string(r[:139]) + "…"
	}
	return s
}
