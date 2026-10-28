package clients

import (
	"context"
	"fmt"

	"github.com/pmaxis/helpic/internal/i18n"
	"github.com/pmaxis/helpic/internal/mail"
)

// emailInvitation sends the invitation's link to the client, in the
// freelancer's language (the client has no account to have one yet). The link
// goes only to the invited address: opening it is the proof the email is
// theirs. The token is the last part of the link.
func (s *Service) emailInvitation(ctx context.Context, freelancerID string, inv Invitation, token string) error {
	from, err := s.repo.PersonName(ctx, freelancerID)
	if err != nil {
		return err
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
		Button: &mail.Button{Text: t("Accept the invitation"), URL: s.appURL + "/invite/" + token},
		Note:   t("The link works once, until %s. If you weren't expecting this, ignore this email.", i18n.Date(locale, inv.ExpiresAt)),
	}); err != nil {
		return err
	}
	return nil
}

// PersonName is how an account is shown: its name, or its email.
func (r *Repository) PersonName(ctx context.Context, userID string) (string, error) {
	var name string
	if err := r.db.QueryRow(ctx, `SELECT coalesce(nullif(name, ''), email) FROM users WHERE id = $1`, userID).Scan(&name); err != nil {
		return "", fmt.Errorf("get person name: %w", err)
	}
	return name, nil
}

// emailFreelancerInvitation sends the client's invitation link to the
// freelancer, in the client's language. Like a freelancer's invitation, the
// link goes only to the invited address.
func (s *Service) emailFreelancerInvitation(ctx context.Context, clientID string, inv FreelancerInvitation, token string) error {
	from, err := s.repo.PersonName(ctx, clientID)
	if err != nil {
		return err
	}
	company, err := s.repo.CompanyName(ctx, clientID)
	if err != nil {
		return err
	}
	locale := i18n.FromContext(ctx)
	t := func(text string, args ...any) string { return i18n.T(locale, text, args...) }
	if company != "" {
		from = t("%s from %s", from, company)
	}
	greeting := t("Hi,")
	if inv.Name != "" {
		greeting = t("Hi %s,", inv.Name)
	}
	return s.mail.Send(ctx, mail.Email{
		To:      inv.Email,
		Locale:  locale,
		Subject: t("%s invited you to Helpic", from),
		Heading: t("%s wants to work with you on Helpic", from),
		Paragraphs: []string{
			greeting,
			t("%s gives tasks to their freelancers in Helpic. Accept the invitation to see the tasks, track your time and send reports in one place.", from),
		},
		Button: &mail.Button{Text: t("Accept the invitation"), URL: s.appURL + "/freelancer-invite/" + token},
		Note:   t("The link works once, until %s. If you weren't expecting this, ignore this email.", i18n.Date(locale, inv.ExpiresAt)),
	})
}

// CompanyName is the name of the company the client owns, "" without one.
func (r *Repository) CompanyName(ctx context.Context, clientID string) (string, error) {
	var name string
	if err := r.db.QueryRow(ctx, `SELECT coalesce((SELECT name FROM companies WHERE owner_id = $1), '')`, clientID).Scan(&name); err != nil {
		return "", fmt.Errorf("get company name: %w", err)
	}
	return name, nil
}
