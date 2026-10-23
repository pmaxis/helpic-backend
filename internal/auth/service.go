package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/pmaxis/helpic/internal/i18n"
	"github.com/pmaxis/helpic/internal/ratelimit"
	"github.com/pmaxis/helpic/internal/user"
)

// repository is the persistence contract the service depends on. It is defined
// here (consumer side) but implemented by *Repository.
type repository interface {
	CreateUser(ctx context.Context, email, name, role, passwordHash, locale string) (user.User, error)
	DeleteUser(ctx context.Context, id string) error
	GetByEmail(ctx context.Context, email string) (user.User, error)
	GetByID(ctx context.Context, id string) (user.User, error)

	CreateSession(ctx context.Context, userID string, refreshHash []byte, c Client, expiresAt time.Time) (Session, error)
	Rotate(ctx context.Context, old, next []byte, expiresAt time.Time) (Session, error)
	Replaced(ctx context.Context, hash []byte) (Session, time.Time, error)
	SessionRole(ctx context.Context, userID, sessionID string) (string, error)
	DeleteSession(ctx context.Context, id string) error
	DeleteByRefresh(ctx context.Context, hash []byte) error
	ListSessions(ctx context.Context, userID string) ([]Session, error)
	DeleteUserSession(ctx context.Context, userID, id string) error
	DeleteOtherSessions(ctx context.Context, userID, keepID string) error
	DeleteExpiredSessions(ctx context.Context) error

	CreateReset(ctx context.Context, userID string, hash []byte, expiresAt time.Time) error
	TakeReset(ctx context.Context, hash []byte) (userID, email, locale string, err error)
	SetPasswordAndSignOut(ctx context.Context, userID, passwordHash string) error
	DeleteExpiredResets(ctx context.Context) error

	SavePendingSignup(ctx context.Context, email, name, passwordHash, locale string, hash []byte, expiresAt time.Time) error
	RenewPendingSignup(ctx context.Context, email string, hash []byte, expiresAt time.Time) (string, error)
	TakePendingSignup(ctx context.Context, hash []byte) (email, name, passwordHash, locale string, err error)
	DeletePendingSignup(ctx context.Context, email string, hash []byte) error
	DeleteExpiredSignups(ctx context.Context) error

	SetPendingSecret(ctx context.Context, userID string, sealed []byte) error
	PendingSecret(ctx context.Context, userID string) ([]byte, error)
	ConfirmTwoFactor(ctx context.Context, userID string, pending []byte, step int64, hashes [][]byte) error
	Secret(ctx context.Context, userID string) ([]byte, int64, error)
	UseStep(ctx context.Context, userID string, step int64) (bool, error)
	UseRecoveryCode(ctx context.Context, userID string, hash []byte) (bool, error)
	RecoveryCodesLeft(ctx context.Context, userID string) (int, error)
	ReplaceRecoveryCodes(ctx context.Context, userID string, hashes [][]byte) error
	DisableTwoFactor(ctx context.Context, userID string) error
}

// tokenIssuer issues signed access JWTs. Implemented by *token.Manager.
type tokenIssuer interface {
	Generate(userID, sessionID string) (string, time.Time, error)
}

// dummyHash is a valid bcrypt hash compared against when the email is unknown,
// so login timing does not reveal whether an account exists.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("dummy-password-for-timing"), bcrypt.DefaultCost)

// Service contains authentication business logic: password hashing and
// verification, registration rules, sessions (see session.go) and two-factor
// sign-in (twofactor.go).
type Service struct {
	repo   repository
	tokens tokenIssuer
	// refreshTTL is how long a session lasts unused; every refresh extends it.
	refreshTTL time.Duration
	// mail sends reset links; appURL is the frontend they point to.
	mail   Mailer
	appURL string
	// sealer seals authenticator secrets; codeGuesses counts wrong codes
	// per account, nil doesn't limit them (see UseTwoFactor).
	sealer      *Sealer
	codeGuesses ratelimit.Gate
}

// NewService creates an auth Service.
func NewService(repo repository, tokens tokenIssuer, refreshTTL time.Duration, mail Mailer, appURL string) *Service {
	return &Service{repo: repo, tokens: tokens, refreshTTL: refreshTTL, mail: mail, appURL: strings.TrimRight(appURL, "/")}
}

// Register creates a new account with the given role. Public sign-up always
// passes user.RoleFreelancer; invitations create clients. The email is expected
// to be normalized by the caller's validation step. The account speaks the
// request's language. Returns ErrEmailExists on a duplicate.
func (s *Service) Register(ctx context.Context, p RegisterParams, role string) (user.User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(p.Password), bcrypt.DefaultCost)
	if err != nil {
		return user.User{}, fmt.Errorf("hash password: %w", err)
	}

	u, err := s.repo.CreateUser(ctx, p.Email, p.Name, role, string(hash), i18n.FromContext(ctx))
	if errors.Is(err, ErrEmailExists) {
		return user.User{}, ErrEmailExists
	}
	if err != nil {
		return user.User{}, err
	}
	return u, nil
}

// Discard deletes an account Register has just made, when what it was made
// for (an invitation used up in the meantime) didn't happen. Nothing refers
// to it yet.
func (s *Service) Discard(ctx context.Context, userID string) error {
	return s.repo.DeleteUser(ctx, userID)
}

// Login verifies credentials and, on success, starts a session for the device
// and returns the user. A wrong password and an unknown email both yield
// ErrInvalidCredentials. An account with two-factor on also needs a code:
// without one, ErrTwoFactorRequired (said only once the password is right);
// a wrong one, ErrTwoFactorCode or, after too many, a *CodeLimitError.
func (s *Service) Login(ctx context.Context, p LoginParams, c Client) (user.User, Tokens, error) {
	u, err := s.repo.GetByEmail(ctx, p.Email)
	if errors.Is(err, user.ErrNotFound) {
		// Perform a dummy hash comparison to keep timing consistent.
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(p.Password))
		return user.User{}, Tokens{}, ErrInvalidCredentials
	}
	if err != nil {
		return user.User{}, Tokens{}, err
	}

	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(p.Password)); err != nil {
		return user.User{}, Tokens{}, ErrInvalidCredentials
	}
	if u.TwoFactor {
		if err := s.secondFactor(ctx, u.ID, p.Code); err != nil {
			return user.User{}, Tokens{}, err
		}
	}

	tokens, err := s.StartSession(ctx, u, c)
	if err != nil {
		return user.User{}, Tokens{}, err
	}
	return u, tokens, nil
}
