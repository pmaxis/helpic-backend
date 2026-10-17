// Package team gives a client a company and its people. The client a
// freelancer invited owns the company; they invite members — employees or
// contractors, one "member" role for now — by link. Members act for the
// company: they see its freelancers, tasks, projects and boards and set tasks
// for its freelancers (see the acting_client SQL function). Reports stay the
// owner's alone.
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
	JoinedAt time.Time
}

// Invite is a pending invitation to join the company.
type Invite struct {
	ID        string
	CompanyID string
	Email     string
	Name      string
	Token     string
	ExpiresAt time.Time
	CreatedAt time.Time
}

// Expired reports whether the invitation can no longer be accepted.
func (i Invite) Expired() bool { return time.Now().After(i.ExpiresAt) }

// Overview is the team as the viewer sees it. A member sees the company and
// its owner; the owner also sees the members and pending invitations.
type Overview struct {
	// Role is "owner" or "member".
	Role    string
	Name    string
	Owner   Person
	Members []Member
	Invites []Invite
}

// Team roles.
const (
	RoleOwner  = "owner"
	RoleMember = "member"
)
