package clients

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/pmaxis/helpic/internal/i18n"
	"github.com/pmaxis/helpic/internal/mail"
)

// emailInvitation sends the invitation's link to the client, in the
// freelancer's language (the client has no account to have one yet). The link
// goes only to the invited address: opening it is the proof the email is
// theirs. The token rides in the fragment, which browsers send to no server.
// A failure is only logged; the freelancer can invite again.
func (s *Service) emailInvitation(ctx context.Context, freelancerID string, inv Invitation, token string) {
	from, err := s.repo.PersonName(ctx, freelancerID)
	if err != nil {
		slog.Error("failed to email invitation", "error", err)
		return
	}
	locale := i18n.FromContext(ctx)
	t := func(text string, args ...any) string { return i18n.T(locale, text, args...) }
	greeting := t("Hi,")
	if inv.Name != "" {
		greeting = t("Hi %s,", inv.Name)
	}
	if err := s.mail.Send(ctx, mail.Email{
		To:      inv.Email,
		Locale:  locale,
		Subject: t("%s invited you to Helpic", from),
		Heading: t("%s wants to work with you on Helpic", from),
		Paragraphs: []string{
			greeting,
			t("%s uses Helpic to keep client work in one place. Accept the invitation to give them tasks, follow the progress and get their reports.", from),
		},
		Button: &mail.Button{Text: t("Accept the invitation"), URL: s.appURL + "/invite#" + token},
		Note:   t("The link works once, until %s. If you weren't expecting this, ignore this email.", i18n.Date(locale, inv.ExpiresAt)),
	}); err != nil {
		slog.Error("failed to email invitation", "error", err)
	}
}

// PersonName is how an account is shown: its name, or its email.
func (r *Repository) PersonName(ctx context.Context, userID string) (string, error) {
	var name string
	if err := r.db.QueryRow(ctx, `SELECT coalesce(nullif(name, ''), email) FROM users WHERE id = $1`, userID).Scan(&name); err != nil {
		return "", fmt.Errorf("get person name: %w", err)
	}
	return name, nil
}
