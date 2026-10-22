package user

import (
	"errors"
	"time"
)

// ErrNotFound is returned when a user cannot be located.
var ErrNotFound = errors.New("user not found")

// Account roles. Public sign-up always creates a freelancer; a client account
// is created by accepting a freelancer's invitation.
const (
	RoleFreelancer = "freelancer"
	RoleClient     = "client"
)

// User is the domain model for an account. PasswordHash never leaves the
// service layer.
type User struct {
	ID        string
	Email     string
	FirstName string
	LastName  string
	// Name is the full name, computed by the database from the first and last.
	Name string
	Role string
	// Currency is what a freelancer bills in (see package money).
	Currency string
	// HourlyRateCents is what a freelancer charges for an hour, in cents of
	// Currency; nil while they have no rate.
	HourlyRateCents *int64
	// Locale is the language they read Helpic in (see package i18n).
	Locale string
	// TeamOwnerID is set for a member of a client's company: its owner.
	// TeamName is that company's name, TeamRole their role in it ("member"
	// or "contractor").
	TeamOwnerID *string
	TeamName    string
	TeamRole    string
	// TwoFactor is on when signing in also takes a code from an
	// authenticator app (see package auth).
	TwoFactor    bool
	PasswordHash string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Columns is the users column list matching Scan, for packages that read users.
const Columns = `id, email, first_name, last_name, name, role, currency, hourly_rate_cents, locale,
	(SELECT c.owner_id::text FROM company_members m JOIN companies c ON c.id = m.company_id WHERE m.user_id = users.id),
	coalesce((SELECT c.name FROM company_members m JOIN companies c ON c.id = m.company_id WHERE m.user_id = users.id), ''),
	coalesce((SELECT m.role FROM company_members m WHERE m.user_id = users.id), ''),
	two_factor_secret IS NOT NULL, password_hash, created_at, updated_at`

// Scan reads one users row selected with Columns.
func Scan(row interface{ Scan(dest ...any) error }) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Email, &u.FirstName, &u.LastName, &u.Name, &u.Role, &u.Currency, &u.HourlyRateCents, &u.Locale, &u.TeamOwnerID, &u.TeamName, &u.TeamRole, &u.TwoFactor, &u.PasswordHash, &u.CreatedAt, &u.UpdatedAt)
	return u, err
}
