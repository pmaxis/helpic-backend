package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/pmaxis/helpic/internal/user"
)

// repository is the persistence contract the service depends on. It is defined
// here (consumer side) but implemented by *Repository.
type repository interface {
	CreateUser(ctx context.Context, email, name, role, passwordHash string) (user.User, error)
	GetByEmail(ctx context.Context, email string) (user.User, error)
}

// tokenIssuer issues signed JWTs. Implemented by *token.Manager.
type tokenIssuer interface {
	Generate(userID, role string) (string, time.Time, error)
}

// dummyHash is a valid bcrypt hash compared against when the email is unknown,
// so login timing does not reveal whether an account exists.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("dummy-password-for-timing"), bcrypt.DefaultCost)

// Service contains authentication business logic: password hashing and
// verification, registration rules and JWT issuance.
type Service struct {
	repo   repository
	tokens tokenIssuer
}

// NewService creates an auth Service.
func NewService(repo repository, tokens tokenIssuer) *Service {
	return &Service{repo: repo, tokens: tokens}
}

// Register creates a new account with the given role. Public sign-up always
// passes user.RoleFreelancer; invitations create clients. The email is expected
// to be normalized by the caller's validation step. Returns ErrEmailExists on a
// duplicate.
func (s *Service) Register(ctx context.Context, p RegisterParams, role string) (user.User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(p.Password), bcrypt.DefaultCost)
	if err != nil {
		return user.User{}, fmt.Errorf("hash password: %w", err)
	}

	u, err := s.repo.CreateUser(ctx, p.Email, p.Name, role, string(hash))
	if errors.Is(err, ErrEmailExists) {
		return user.User{}, ErrEmailExists
	}
	if err != nil {
		return user.User{}, err
	}
	return u, nil
}

// Login verifies credentials and, on success, issues a JWT and returns the
// user. A wrong password and an unknown email both yield ErrInvalidCredentials.
func (s *Service) Login(ctx context.Context, p LoginParams) (user.User, string, time.Time, error) {
	u, err := s.repo.GetByEmail(ctx, p.Email)
	if errors.Is(err, user.ErrNotFound) {
		// Perform a dummy hash comparison to keep timing consistent.
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(p.Password))
		return user.User{}, "", time.Time{}, ErrInvalidCredentials
	}
	if err != nil {
		return user.User{}, "", time.Time{}, err
	}

	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(p.Password)); err != nil {
		return user.User{}, "", time.Time{}, ErrInvalidCredentials
	}

	jwtString, expiresAt, err := s.IssueToken(u)
	if err != nil {
		return user.User{}, "", time.Time{}, err
	}
	return u, jwtString, expiresAt, nil
}

// IssueToken signs a session token for an account that is already verified,
// e.g. one just created from an invitation.
func (s *Service) IssueToken(u user.User) (string, time.Time, error) {
	return s.tokens.Generate(u.ID, u.Role)
}
