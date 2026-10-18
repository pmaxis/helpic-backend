package team

import (
	"context"
	"log/slog"

	"github.com/pmaxis/helpic/internal/i18n"
	"github.com/pmaxis/helpic/internal/mail"
)

// emailInvite sends the invitation's link to the new member, in the owner's
// language. The link goes only to the invited address: opening it is the
// proof the email is theirs. The token rides in the fragment, which browsers
// send to no server. A failure is only logged; the owner can invite again.
func (s *Service) emailInvite(ctx context.Context, ownerID string, inv Invite, token string) {
	owner, err := s.repo.Person(ctx, ownerID)
	if err != nil {
		slog.Error("failed to email team invite", "error", err)
		return
	}
	who := owner.Name
	if who == "" {
		who = owner.Email
	}
	_, company, _, err := s.repo.Company(ctx, ownerID)
	if err != nil {
		slog.Error("failed to email team invite", "error", err)
		return
	}
	locale := i18n.FromContext(ctx)
	t := func(text string, args ...any) string { return i18n.T(locale, text, args...) }
	if company == "" {
		company = t("%s's team", who)
	}
	// A contractor sees only their own tasks, so the email promises no more.
	why := t("Join to see the work %s does with its freelancers on Helpic, and to set tasks yourself.", company)
	if inv.Role == RoleContractor {
		why = t("Join as a contractor to set tasks for %s's freelancers and follow the ones you're part of.", company)
	}
	if err := s.mail.Send(ctx, mail.Email{
		To:         inv.Email,
		Locale:     locale,
		Subject:    t("Join %s on Helpic", company),
		Heading:    t("%s invited you to %s", who, company),
		Paragraphs: []string{why},
		Button:     &mail.Button{Text: t("Join the team"), URL: s.appURL + "/join#" + token},
		Note:       t("The link works once, until %s. If you weren't expecting this, ignore this email.", i18n.Date(locale, inv.ExpiresAt)),
	}); err != nil {
		slog.Error("failed to email team invite", "error", err)
	}
}
