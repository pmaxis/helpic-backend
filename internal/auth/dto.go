package auth

import (
	"net/mail"
	"strings"
	"unicode/utf8"
)

// RegisterRequest is the JSON body for POST /api/v1/auth/register.
type RegisterRequest struct {
	Email                string `json:"email"`
	Name                 string `json:"name"`
	Password             string `json:"password"`
	PasswordConfirmation string `json:"password_confirmation"`
}

// LoginRequest is the JSON body for POST /api/v1/auth/login.
type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// NormalizeEmail lowercases and trims an email so that "User@Example.com" and
// "user@example.com" are treated as the same account.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// Validate checks the register payload and returns per-field error messages.
// The returned map is empty when the request is valid.
func (r RegisterRequest) Validate() (RegisterParams, map[string]string) {
	fields := map[string]string{}
	email := NormalizeEmail(r.Email)

	if email == "" {
		fields["email"] = "Email is required"
	} else if _, err := mail.ParseAddress(email); err != nil {
		fields["email"] = "Enter a valid email address"
	}

	name := strings.TrimSpace(r.Name)
	if msg := ValidateName(name); msg != "" {
		fields["name"] = msg
	}
	ValidateNewPassword(fields, r.Password, r.PasswordConfirmation)

	if len(fields) > 0 {
		return RegisterParams{}, fields
	}
	return RegisterParams{Email: email, Name: name, Password: r.Password}, nil
}

// ValidateName checks a trimmed display name and returns an error message, or
// "" when it is valid.
func ValidateName(name string) string {
	switch {
	case name == "":
		return "Name is required"
	case utf8.RuneCountInString(name) > MaxNameLength:
		return "Name is too long"
	}
	return ""
}

// ValidateNewPassword checks a new password and its confirmation, adding
// messages for the "password" and "password_confirmation" fields.
func ValidateNewPassword(fields map[string]string, password, confirmation string) {
	if password == "" {
		fields["password"] = "Password is required"
	} else if len(password) < MinPasswordLength {
		fields["password"] = "Password must be at least 8 characters"
	}

	if confirmation == "" {
		fields["password_confirmation"] = "Password confirmation is required"
	} else if password != confirmation {
		fields["password_confirmation"] = "Passwords do not match"
	}
}

// Validate checks the login payload.
func (r LoginRequest) Validate() (LoginParams, map[string]string) {
	fields := map[string]string{}
	email := NormalizeEmail(r.Email)

	if email == "" {
		fields["email"] = "Email is required"
	}
	if r.Password == "" {
		fields["password"] = "Password is required"
	}

	if len(fields) > 0 {
		return LoginParams{}, fields
	}
	return LoginParams{Email: email, Password: r.Password}, nil
}
