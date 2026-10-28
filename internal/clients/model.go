// Package clients links freelancers with their clients. Either side invites
// the other by email: a freelancer their client, a client their freelancer.
// The invited person accepts through the link, creating an account (or
// signing in to an existing one), and from then on the two are linked.
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
	// ErrSelfInvite is returned when someone invites their own email.
	ErrSelfInvite = errors.New("cannot invite yourself")
	// ErrNotFreelancerEmail is returned when a client invites an email that
	// belongs to a client's or an admin's account.
	ErrNotFreelancerEmail = errors.New("email belongs to an account that isn't a freelancer's")
	// ErrAlreadyFreelancer is returned when the client already works with
	// the freelancer.
	ErrAlreadyFreelancer = errors.New("already working with this freelancer")
	// ErrNotOwner is returned when a member of a client's company invites a
	// freelancer: the company's owner chooses who it works with.
	ErrNotOwner = errors.New("only the company's owner invites freelancers")
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

// FreelancerInvitation is a client's pending offer for a freelancer to work
// with them.
type FreelancerInvitation struct {
	ID       string
	ClientID string
	Email    string
	// Name is the freelancer's name as the client entered it; it pre-fills
	// the freelancer's sign-up form.
	Name       string
	ExpiresAt  time.Time
	AcceptedAt *time.Time
	CreatedAt  time.Time
}

// Expired reports whether the invitation can no longer be accepted.
func (i FreelancerInvitation) Expired() bool {
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
