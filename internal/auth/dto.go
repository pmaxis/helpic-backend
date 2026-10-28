package auth

import (
	"net/mail"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/pmaxis/helpic/internal/user"
)

// RegisterRequest is the JSON body for POST /api/v1/auth/register. Role is
// "freelancer" (the default when left out) or "client".
type RegisterRequest struct {
	Email                string `json:"email"`
	Name                 string `json:"name"`
	Password             string `json:"password"`
	PasswordConfirmation string `json:"password_confirmation"`
	Role                 string `json:"role,omitempty" enums:"freelancer,client"`
}

// LoginRequest is the JSON body for POST /api/v1/auth/login.
type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	// Code is asked for once the password is right, when the account has
	// two-factor on: six digits from the app, or a recovery code.
	Code string `json:"code,omitempty"`
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

	role := r.Role
	switch role {
	case "":
		role = user.RoleFreelancer
	case user.RoleFreelancer, user.RoleClient:
	default:
		fields["role"] = "Sign up as a freelancer or a client"
	}

	if len(fields) > 0 {
		return RegisterParams{}, fields
	}
	return RegisterParams{Email: email, Name: name, Password: r.Password, Role: role}, nil
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
	if msg := passwordIssue(password); msg != "" {
		fields["password"] = msg
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
	return LoginParams{Email: email, Password: r.Password, Code: r.Code}, nil
}

// SessionDTO is a signed-in device as the API shows it.
type SessionDTO struct {
	ID         string    `json:"id"`
	UserAgent  string    `json:"user_agent"`
	IP         string    `json:"ip"`
	Current    bool      `json:"current"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// ToSessionDTO converts a session; current is the requesting device's session.
func ToSessionDTO(s Session, current string) SessionDTO {
	return SessionDTO{
		ID:         s.ID,
		UserAgent:  s.UserAgent,
		IP:         s.IP,
		Current:    s.ID == current,
		CreatedAt:  s.CreatedAt,
		LastUsedAt: s.LastUsedAt,
		ExpiresAt:  s.ExpiresAt,
	}
}

// ForgotRequest is the JSON body for POST /api/v1/auth/password/forgot.
type ForgotRequest struct {
	Email string `json:"email"`
}

// Validate returns the normalized email and per-field errors.
func (r ForgotRequest) Validate() (string, map[string]string) {
	email := NormalizeEmail(r.Email)
	switch {
	case email == "":
		return "", map[string]string{"email": "Email is required"}
	case !validEmail(email):
		return "", map[string]string{"email": "Enter a valid email address"}
	}
	return email, nil
}

// SignupConfirmRequest is the JSON body for POST /api/v1/auth/signups/confirm.
type SignupConfirmRequest struct {
	Token string `json:"token"`
}

// ResetRequest is the JSON body for POST /api/v1/auth/password/reset.
type ResetRequest struct {
	Token                string `json:"token"`
	Password             string `json:"password"`
	PasswordConfirmation string `json:"password_confirmation"`
}

// Validate returns per-field errors.
func (r ResetRequest) Validate() map[string]string {
	fields := map[string]string{}
	ValidateNewPassword(fields, r.Password, r.PasswordConfirmation)
	return fields
}

// passwordIssue reports why a new password is refused, or "" when it is
// long enough, short enough for bcrypt, and contains a letter and a number.
// Letters and numbers of any alphabet count.
func passwordIssue(password string) string {
	switch {
	case password == "":
		return "Password is required"
	case utf8.RuneCountInString(password) < MinPasswordLength:
		return "Password must be at least 8 characters"
	case len(password) > MaxPasswordBytes:
		return "Password is too long"
	case !hasLetterAndNumber(password):
		return "Password must include a letter and a number"
	}
	return ""
}

func hasLetterAndNumber(password string) bool {
	var letter, number bool
	for _, r := range password {
		switch {
		case unicode.IsLetter(r):
			letter = true
		case unicode.IsNumber(r):
			number = true
		}
		if letter && number {
			return true
		}
	}
	return false
}

func validEmail(email string) bool {
	_, err := mail.ParseAddress(email)
	return err == nil
}

// TwoFactorDTO is where the account's two-factor sign-in stands.
type TwoFactorDTO struct {
	Enabled bool `json:"enabled"`
	// RecoveryCodesLeft is how many unused recovery codes there are; 0 while off.
	RecoveryCodesLeft int `json:"recovery_codes_left"`
}

// TwoFactorSetupDTO is what goes into the authenticator app.
type TwoFactorSetupDTO struct {
	// Secret is for typing in by hand, base32.
	Secret string `json:"secret"`
	// URI is the otpauth:// link the QR code carries.
	URI string `json:"uri"`
	// QR is an SVG image of URI, as a data: URL.
	QR string `json:"qr"`
}

// RecoveryCodesDTO are new recovery codes, shown this once.
type RecoveryCodesDTO struct {
	RecoveryCodes []string `json:"recovery_codes"`
}

// TwoFactorOnDTO is the account with two-factor just turned on, and its
// recovery codes, shown this once.
type TwoFactorOnDTO struct {
	User          user.DTO `json:"user"`
	RecoveryCodes []string `json:"recovery_codes"`
}

// PasswordConfirmRequest is the JSON body for POST /api/v1/me/two-factor/setup.
type PasswordConfirmRequest struct {
	CurrentPassword string `json:"current_password"`
}

// Validate returns per-field errors.
func (r PasswordConfirmRequest) Validate() map[string]string {
	fields := map[string]string{}
	if r.CurrentPassword == "" {
		fields["current_password"] = "Enter your current password"
	}
	return fields
}

// TwoFactorCodeRequest is the JSON body for POST /api/v1/me/two-factor/enable.
type TwoFactorCodeRequest struct {
	Code string `json:"code"`
}

// Validate returns per-field errors.
func (r TwoFactorCodeRequest) Validate() map[string]string {
	fields := map[string]string{}
	if strings.TrimSpace(r.Code) == "" {
		fields["code"] = "Enter the code from your authenticator app"
	}
	return fields
}

// TwoFactorChangeRequest is the JSON body for turning two-factor off and for
// new recovery codes: the password, and a code from the app or a recovery code.
type TwoFactorChangeRequest struct {
	CurrentPassword string `json:"current_password"`
	Code            string `json:"code"`
}

// Validate returns per-field errors.
func (r TwoFactorChangeRequest) Validate() map[string]string {
	fields := PasswordConfirmRequest{CurrentPassword: r.CurrentPassword}.Validate()
	if strings.TrimSpace(r.Code) == "" {
		fields["code"] = "Enter a code from your authenticator app or a recovery code"
	}
	return fields
}
