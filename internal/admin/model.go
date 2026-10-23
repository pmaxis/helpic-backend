// Package admin is the admin panel: how Helpic is used, across all accounts.
// It is for admins, accounts of their own role made with the cli, who must
// sign in with two-factor. It shows counts and account metadata only, never
// the content of anyone's work; admins themselves are left out of it.
package admin

import (
	"errors"
	"time"
)

// ErrNotFound is returned for an account that doesn't exist.
var ErrNotFound = errors.New("account not found")

// ErrEmailExists is returned when making an admin with an email that already
// has an account.
var ErrEmailExists = errors.New("an account with this email already exists")

// ErrInvalidEmail is returned when making an admin with something that isn't
// an email address.
var ErrInvalidEmail = errors.New("not an email address")

// Totals is the app as it stands.
type Totals struct {
	Users       int
	Freelancers int
	Clients     int
	// PendingSignups have not opened their confirmation link yet.
	PendingSignups int
	TwoFactor      int

	// Active users: today, the last 7 days and the last 30, today included.
	ActiveDay   int
	ActiveWeek  int
	ActiveMonth int

	Tasks    int
	Projects int
}

// Period is what happened over a stretch of days.
type Period struct {
	// Active is how many people used Helpic on any of the days.
	Active         int
	NewUsers       int
	TasksCreated   int
	TasksCompleted int
}

// Day is one day, in the app's time zone.
type Day struct {
	Day            time.Time
	Signups        int
	Active         int
	TasksCreated   int
	TasksCompleted int
}

// Stats is everything the statistics page shows.
type Stats struct {
	From     time.Time
	To       time.Time
	Totals   Totals
	Period   Period
	Previous Period
	// Days is the period day by day, oldest first; PreviousDays the period
	// before it, as long.
	Days         []Day
	PreviousDays []Day
}

// Account is a user as the admin panel lists them: who they are and how much
// they use Helpic, nothing of what they work on.
type Account struct {
	ID        string
	Email     string
	Name      string
	Role      string
	TwoFactor bool
	// Tasks they work on, on either side.
	Tasks    int
	Projects int
	// ActiveDays is on how many of the last 30 days they used Helpic.
	ActiveDays int
	LastActive *time.Time
	CreatedAt  time.Time
}

// Sort orders of the accounts list.
const (
	SortCreated    = "created"
	SortLastActive = "last_active"
	SortTasks      = "tasks"
)

// Activity filters of the accounts list.
const (
	// ActivityActive used Helpic in the last 7 days.
	ActivityActive = "active"
	// ActivityIdle used it once, but not in the last 30 days.
	ActivityIdle = "idle"
	// ActivityNever never used it signed in.
	ActivityNever = "never"
)

// AccountFilter picks a page of accounts.
type AccountFilter struct {
	// Query matches the email or the name, any part of them.
	Query    string
	Role     string
	Activity string
	Sort     string
	Page     int
	Limit    int
}

// Team is the client company an account owns or belongs to.
type Team struct {
	Name string
	// Role is "owner", "member" or "contractor".
	Role    string
	Members int
}

// AccountDetail is one account in full, still without anyone's work.
type AccountDetail struct {
	Account
	Locale   string
	Currency string
	Team     *Team
	// Partners are the clients a freelancer works with, or a client's
	// freelancers.
	Partners      int
	ReportsSent   int
	ReportsPaid   int
	SecondsLogged int64
	Comments      int
	Telegram      bool
	// Sessions are their signed-in devices now; LastSignIn the newest's start.
	Sessions   int
	LastSignIn *time.Time
	// ActiveOn are the days they used Helpic since ActiveSince, oldest first.
	ActiveSince time.Time
	ActiveOn    []time.Time
}
