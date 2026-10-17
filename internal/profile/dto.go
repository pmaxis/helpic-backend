package profile

import (
	"net/mail"
	"strings"
	"unicode/utf8"

	"github.com/pmaxis/helpic/internal/auth"
	"github.com/pmaxis/helpic/internal/money"
)

// NameRequest is the JSON body for PUT /api/v1/me.
type NameRequest struct {
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

// Name is a validated first and last name; the last one may be empty (a
// company, or someone who goes by one name).
type Name struct {
	First string
	Last  string
}

// Validate returns the trimmed names and per-field errors.
func (r NameRequest) Validate() (Name, map[string]string) {
	n := Name{First: strings.TrimSpace(r.FirstName), Last: strings.TrimSpace(r.LastName)}
	fields := map[string]string{}
	switch {
	case n.First == "":
		fields["first_name"] = "First name is required"
	case utf8.RuneCountInString(n.First) > auth.MaxNameLength:
		fields["first_name"] = "First name is too long"
	}
	if utf8.RuneCountInString(n.Last) > auth.MaxNameLength {
		fields["last_name"] = "Last name is too long"
	}
	return n, fields
}

// EmailRequest is the JSON body for PUT /api/v1/me/email.
type EmailRequest struct {
	Email           string `json:"email"`
	CurrentPassword string `json:"current_password"`
}

// Validate returns the normalized email and per-field errors.
func (r EmailRequest) Validate() (string, map[string]string) {
	email := auth.NormalizeEmail(r.Email)
	fields := map[string]string{}
	if email == "" {
		fields["email"] = "Email is required"
	} else if _, err := mail.ParseAddress(email); err != nil {
		fields["email"] = "Enter a valid email address"
	}
	if r.CurrentPassword == "" {
		fields["current_password"] = "Enter your current password"
	}
	return email, fields
}

// PasswordRequest is the JSON body for PUT /api/v1/me/password.
type PasswordRequest struct {
	CurrentPassword      string `json:"current_password"`
	Password             string `json:"password"`
	PasswordConfirmation string `json:"password_confirmation"`
}

// Validate returns per-field errors.
func (r PasswordRequest) Validate() map[string]string {
	fields := map[string]string{}
	if r.CurrentPassword == "" {
		fields["current_password"] = "Enter your current password"
	}
	auth.ValidateNewPassword(fields, r.Password, r.PasswordConfirmation)
	return fields
}

// CurrencyRequest is the JSON body for PUT /api/v1/me/currency.
type CurrencyRequest struct {
	Currency string `json:"currency"`
}

// Validate returns the upper-cased currency code and per-field errors.
func (r CurrencyRequest) Validate() (string, map[string]string) {
	code := strings.ToUpper(strings.TrimSpace(r.Currency))
	fields := map[string]string{}
	if !money.ValidCurrency(code) {
		fields["currency"] = "Choose one of the currencies on the list"
	}
	return code, fields
}

// PendingEmailDTO answers an email change: where the confirmation went.
type PendingEmailDTO struct {
	PendingEmail string `json:"pending_email"`
}

// ConfirmEmailRequest is the JSON body for POST /api/v1/email-changes/confirm.
type ConfirmEmailRequest struct {
	Token string `json:"token"`
}

// LocaleRequest is the JSON body for PUT /api/v1/me/locale.
type LocaleRequest struct {
	Locale string `json:"locale"`
}
