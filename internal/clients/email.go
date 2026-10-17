package clients

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/pmaxis/helpic/internal/mail"
)

// emailInvitation sends the invitation's link to the client. The freelancer
// can still copy the link, so a failure is only logged.
func (s *Service) emailInvitation(ctx context.Context, freelancerID string, inv Invitation) {
	from, err := s.repo.PersonName(ctx, freelancerID)
	if err != nil {
		slog.Error("failed to email invitation", "error", err)
		return
	}
	greeting := "Hi,"
	if inv.Name != "" {
		greeting = "Hi " + inv.Name + ","
	}
	if err := s.mail.Send(ctx, mail.Email{
		To:      inv.Email,
		Subject: from + " invited you to Helpic",
		Heading: from + " wants to work with you on Helpic",
		Paragraphs: []string{
			greeting,
			from + " uses Helpic to keep client work in one place. Accept the invitation to give them tasks, follow the progress and get their reports.",
		},
		Button: &mail.Button{Text: "Accept the invitation", URL: s.appURL + "/invite/" + inv.Token},
		Note:   fmt.Sprintf("The link works once, until %s. If you weren't expecting this, ignore this email.", inv.ExpiresAt.Format("2 Jan 2006")),
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
