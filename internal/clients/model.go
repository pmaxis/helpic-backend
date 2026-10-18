// Package clients links freelancers with their clients. A freelancer invites a
// client by email; the client accepts through the invitation link, creating a
// client account (or signing in to an existing one), and from then on the two
// are linked.
package clients

import (
	"errors"
	"time"
)

// InvitationTTL is how long an invitation link stays valid.
const InvitationTTL = 14 * 24 * time.Hour

var (
	ErrNotFound = errors.New("not found")
	// ErrInvitationInvalid covers unknown, accepted and expired invitations.
	ErrInvitationInvalid = errors.New("invitation is invalid or expired")
	ErrAlreadyInvited    = errors.New("an invitation for this email is already pending")
	ErrAlreadyClient     = errors.New("already linked to this client")
	// ErrNotClientEmail is returned when the email belongs to a freelancer
	// account, which cannot become a client.
	ErrNotClientEmail = errors.New("email belongs to a freelancer account")
	// ErrTeamMember is returned for a member of a client's company: they act
	// for that company and can't be a client of their own.
	ErrTeamMember = errors.New("email belongs to a member of a client's team")
	// ErrSelfInvite is returned when a freelancer invites their own email.
	ErrSelfInvite = errors.New("cannot invite yourself")
)

// Invitation is a freelancer's pending offer for a client to join.
type Invitation struct {
	ID           string
	FreelancerID string
	Email        string
	// Name is the client's name as the freelancer entered it; it pre-fills the
	// client's sign-up form.
	Name       string
	ExpiresAt  time.Time
	AcceptedAt *time.Time
	CreatedAt  time.Time
}

// Expired reports whether the invitation can no longer be accepted.
func (i Invitation) Expired() bool {
	return time.Now().After(i.ExpiresAt)
}

// Party is the public side of an account another user sees.
type Party struct {
	ID    string
	Name  string
	Email string
}

// Link is the other side of a freelancer–client pair as seen by one of them.
type Link struct {
	Party
	LinkedAt  time.Time
	OpenTasks int
}
