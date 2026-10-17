package profile

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"

	"github.com/pmaxis/helpic/internal/user"
)

// ErrWrongPassword is returned when the current password doesn't match.
var ErrWrongPassword = errors.New("current password is wrong")

// Service holds the account-change rules: changing the email or the password
// needs the current password.
type Service struct {
	repo *Repository
}

// NewService creates a profile Service.
func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

// UpdateName changes the user's first and last name.
func (s *Service) UpdateName(ctx context.Context, userID string, n Name) (user.User, error) {
	return s.repo.SetName(ctx, userID, n.First, n.Last)
}

// ChangeEmail changes the sign-in email after checking the current password.
func (s *Service) ChangeEmail(ctx context.Context, userID, email, currentPassword string) (user.User, error) {
	u, err := s.verify(ctx, userID, currentPassword)
	if err != nil {
		return user.User{}, err
	}
	if u.Email == email {
		return u, nil
	}
	return s.repo.SetEmail(ctx, userID, email)
}

// ChangePassword sets a new password after checking the current one.
func (s *Service) ChangePassword(ctx context.Context, userID, currentPassword, newPassword string) (user.User, error) {
	if _, err := s.verify(ctx, userID, currentPassword); err != nil {
		return user.User{}, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return user.User{}, fmt.Errorf("hash password: %w", err)
	}
	return s.repo.SetPasswordHash(ctx, userID, string(hash))
}

// SetCurrency changes the currency a freelancer bills in.
func (s *Service) SetCurrency(ctx context.Context, userID, currency string) (user.User, error) {
	return s.repo.SetCurrency(ctx, userID, currency)
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
