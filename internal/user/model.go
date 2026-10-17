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
	// TeamOwnerID is set for a member of a client's company: its owner.
	// TeamName is that company's name.
	TeamOwnerID  *string
	TeamName     string
	PasswordHash string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Columns is the users column list matching Scan, for packages that read users.
const Columns = `id, email, first_name, last_name, name, role, currency,
	(SELECT c.owner_id::text FROM company_members m JOIN companies c ON c.id = m.company_id WHERE m.user_id = users.id),
	coalesce((SELECT c.name FROM company_members m JOIN companies c ON c.id = m.company_id WHERE m.user_id = users.id), ''),
	password_hash, created_at, updated_at`

// Scan reads one users row selected with Columns.
func Scan(row interface{ Scan(dest ...any) error }) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Email, &u.FirstName, &u.LastName, &u.Name, &u.Role, &u.Currency, &u.TeamOwnerID, &u.TeamName, &u.PasswordHash, &u.CreatedAt, &u.UpdatedAt)
	return u, err
}
