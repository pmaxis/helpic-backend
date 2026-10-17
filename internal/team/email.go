package team

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/pmaxis/helpic/internal/mail"
)

// emailInvite sends the invitation's link to the new member. The owner can
// still copy the link, so a failure is only logged.
func (s *Service) emailInvite(ctx context.Context, ownerID string, inv Invite) {
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
	if company == "" {
		company = who + "'s team"
	}
	if err := s.mail.Send(ctx, mail.Email{
		To:      inv.Email,
		Subject: "Join " + company + " on Helpic",
		Heading: who + " invited you to " + company,
		Paragraphs: []string{
			"Join to see the work " + company + " does with its freelancers on Helpic, and to set tasks yourself.",
		},
		Button: &mail.Button{Text: "Join the team", URL: s.appURL + "/join/" + inv.Token},
		Note:   fmt.Sprintf("The link works once, until %s. If you weren't expecting this, ignore this email.", inv.ExpiresAt.Format("2 Jan 2006")),
	}); err != nil {
		slog.Error("failed to email team invite", "error", err)
	}
}
