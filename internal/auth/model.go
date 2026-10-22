package auth

import "errors"

// Domain errors returned by the auth service. Handlers map these to HTTP status
// codes and safe, non-enumerating messages.
var (
	// ErrInvalidCredentials is returned for both an unknown email and a wrong
	// password, so the API never reveals whether an account exists.
	ErrInvalidCredentials = errors.New("invalid credentials")

	// ErrEmailExists is returned when registering an already-registered email.
	ErrEmailExists = errors.New("email already registered")
)

// RegisterParams is the validated, normalized input to Service.Register.
type RegisterParams struct {
	Email    string
	Name     string
	Password string
}

// LoginParams is the validated, normalized input to Service.Login.
type LoginParams struct {
	Email    string
	Password string
	// Code is from an authenticator app, or a recovery code; needed only
	// when the account has two-factor on.
	Code string
}

// MinPasswordLength is enforced by both the API and the frontend. A new
// password also needs a letter and a number (see passwordIssue).
const MinPasswordLength = 8

// MaxPasswordBytes is as much of a password as bcrypt reads; a longer one
// would fail to hash. Bytes, not letters: "ї" takes two.
const MaxPasswordBytes = 72

// MaxNameLength caps a display name.
const MaxNameLength = 100
