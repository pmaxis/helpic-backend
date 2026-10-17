// Package team gives a client a company and its people. The client a
// freelancer invited owns the company; they invite people by link, as
// members or contractors. Both act for the company: they see its freelancers,
// projects and boards and set tasks for its freelancers (see the
// acting_client SQL function). A member sees all the company's tasks, a
// contractor only the ones they set or were added to (sees_task). Reports
// stay the owner's alone.
package team

import (
	"errors"
	"time"
)

// InviteTTL is how long a team invitation link stays valid.
const InviteTTL = 14 * 24 * time.Hour

var (
	ErrNotFound = errors.New("not found")
	// ErrNotOwner is returned when a member tries to run the team.
	ErrNotOwner = errors.New("only the company's owner manages the team")
	// ErrAccountExists is returned for an email that already has an account:
	// members join with a new one.
	ErrAccountExists = errors.New("email already has an account")
	// ErrAlreadyInvited is returned while an invitation for the email is pending.
	ErrAlreadyInvited = errors.New("an invitation for this email is already pending")
	// ErrInviteInvalid covers unknown, used and expired invitations.
	ErrInviteInvalid = errors.New("invitation is invalid or expired")
)

// Person is someone on the team.
type Person struct {
	ID    string
	Name  string
	Email string
}

// Member is a person who joined the company.
type Member struct {
	Person
	Role     string
	JoinedAt time.Time
}

// Invite is a pending invitation to join the company.
type Invite struct {
	ID        string
	CompanyID string
	Email     string
	Name      string
	Role      string
	Token     string
	ExpiresAt time.Time
	CreatedAt time.Time
}

// Expired reports whether the invitation can no longer be accepted.
func (i Invite) Expired() bool { return time.Now().After(i.ExpiresAt) }

// Overview is the team as the viewer sees it. A member sees the company and
// its owner; the owner also sees the members and pending invitations.
type Overview struct {
	// Role is the viewer's: "owner", "member" or "contractor".
	Role    string
	Name    string
	Owner   Person
	Members []Member
	Invites []Invite
}

// Team roles. The owner invites the others as members or contractors.
const (
	RoleOwner      = "owner"
	RoleMember     = "member"
	RoleContractor = "contractor"
)

// ValidRole reports whether someone can be invited or set as the role.
func ValidRole(role string) bool { return role == RoleMember || role == RoleContractor }
