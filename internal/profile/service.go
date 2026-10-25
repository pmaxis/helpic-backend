package profile

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/pmaxis/helpic/internal/auth"
	"github.com/pmaxis/helpic/internal/user"
)

var (
	// ErrWrongPassword is returned when the current password doesn't match.
	ErrWrongPassword = errors.New("current password is wrong")
	// ErrCurrencyLocked is returned when changing currency would relabel
	// prices that already exist. Amounts are not converted.
	ErrCurrencyLocked = errors.New("prices already exist in the current currency")
	// ErrNotFreelancer is returned when someone who doesn't bill sets a rate.
	ErrNotFreelancer = errors.New("only a freelancer has an hourly rate")
)

// sessions signs the user's other devices out. Implemented by *auth.Service.
type sessions interface {
	EndOtherSessions(ctx context.Context, userID, keepID string) error
}

// Service holds the account-change rules: changing the email or the password
// needs the current password, and a new email needs confirming (email.go).
type Service struct {
	repo     *Repository
	sessions sessions
	// mail sends confirmation links and notices; appURL is the frontend.
	mail   auth.Mailer
	appURL string
}

// NewService creates a profile Service.
func NewService(repo *Repository, sessions sessions, mail auth.Mailer, appURL string) *Service {
	return &Service{repo: repo, sessions: sessions, mail: mail, appURL: strings.TrimRight(appURL, "/")}
}

// UpdateName changes the user's first and last name.
func (s *Service) UpdateName(ctx context.Context, userID string, n Name) (user.User, error) {
	return s.repo.SetName(ctx, userID, n.First, n.Last)
}

// ChangePassword sets a new password after checking the current one, and signs
// out every device but the one making the change (sessionID): whoever knew the
// old password is out.
func (s *Service) ChangePassword(ctx context.Context, userID, sessionID, currentPassword, newPassword string) (user.User, error) {
	if _, err := s.verify(ctx, userID, currentPassword); err != nil {
		return user.User{}, err
	}
	hash, err := auth.HashPassword(newPassword)
	if err != nil {
		return user.User{}, err
	}
	u, err := s.repo.SetPasswordHash(ctx, userID, hash)
	if err != nil {
		return user.User{}, err
	}
	if err := s.sessions.EndOtherSessions(ctx, userID, sessionID); err != nil {
		return user.User{}, err
	}
	if err := s.mail.Send(ctx, auth.PasswordChangedEmail(u.Email, u.Locale)); err != nil {
		slog.Error("failed to queue password changed email", "error", err)
	}
	return u, nil
}

// SetLocale changes the language the user reads Helpic in.
func (s *Service) SetLocale(ctx context.Context, userID, locale string) (user.User, error) {
	return s.repo.SetLocale(ctx, userID, locale)
}

// SetCurrency changes the currency a freelancer bills in. It refuses while any
// of their tasks still has a price: the number would keep its digits and only
// the currency name would change.
func (s *Service) SetCurrency(ctx context.Context, userID, currency string) (user.User, error) {
	u, err := s.repo.Get(ctx, userID)
	if err != nil {
		return user.User{}, err
	}
	if u.Currency == currency {
		return u, nil
	}
	priced, err := s.repo.HasPricedTasks(ctx, userID)
	if err != nil {
		return user.User{}, err
	}
	if priced {
		return user.User{}, ErrCurrencyLocked
	}
	return s.repo.SetCurrency(ctx, userID, currency)
}

// SetHourlyRate sets or clears what a freelancer charges for an hour. Their
// open hourly tasks are priced again at the new rate; billed ones keep theirs.
func (s *Service) SetHourlyRate(ctx context.Context, userID string, cents *int64) (user.User, error) {
	u, err := s.repo.Get(ctx, userID)
	if err != nil {
		return user.User{}, err
	}
	if u.Role != user.RoleFreelancer {
		return user.User{}, ErrNotFreelancer
	}
	return s.repo.SetHourlyRate(ctx, userID, cents)
}

func (s *Service) verify(ctx context.Context, userID, password string) (user.User, error) {
	u, err := s.repo.Get(ctx, userID)
	if err != nil {
		return user.User{}, err
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		return user.User{}, ErrWrongPassword
	}
	return u, nil
}
