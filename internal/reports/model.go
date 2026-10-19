// Package reports lets a freelancer report the work done for a client over a
// period: the tasks completed in it, plus a summary. Making a report snapshots
// those tasks, so a sent report reads the same later. Their prices stay live
// while it's a draft and are frozen, with the currency, when it's sent. A draft is the
// freelancer's to edit; once sent, the client sees it and it no longer changes.
// A report without a client covers personal work; it is only downloaded.
package reports

import (
	"errors"
	"strings"
	"time"
)

// Report statuses.
const (
	StatusDraft = "draft"
	StatusSent  = "sent"
	// StatusPaid: the client marked a sent report paid.
	StatusPaid = "paid"
)

// Report kinds: a basic report lists the tasks; a detailed one adds a note on
// what was done in each.
const (
	KindBasic    = "basic"
	KindDetailed = "detailed"
)

var (
	ErrNotFound = errors.New("report not found")
	// ErrNotLinked is returned for a client the freelancer doesn't work with.
	ErrNotLinked = errors.New("not one of your clients")
	// ErrProjectMismatch is returned for a project that isn't with that client.
	ErrProjectMismatch = errors.New("project is not with this client")
	// ErrSent is returned when changing a report that was already sent.
	ErrSent = errors.New("report was already sent")
	// ErrKept is returned when deleting a report the client has already been sent.
	ErrKept = errors.New("a sent report is kept")
	// ErrNoClient is returned when sending a personal report.
	ErrNoClient = errors.New("a personal report has no one to send it to")
)

// Party is one side of a report.
type Party struct {
	ID    string
	Name  string
	Email string
}

// Item is one completed task in a report, as it was when the report was made.
type Item struct {
	ID          string
	TaskID      string
	Title       string
	ProjectName string
	StatusName  string
	CompletedOn time.Time
	// PriceCents is nil for a task without a price.
	PriceCents *int64
	// Notes say what was done in the task, for a detailed report. They start
	// as the task's description; the freelancer rewrites them in the draft.
	Notes string
}

// ProjectRef is a project a report covers, named as it was when the report
// was made; ID is "" once the project is deleted.
type ProjectRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Report is a freelancer's report of work done for a client.
type Report struct {
	ID         string
	Freelancer Party
	// Client is nil for a report on personal work.
	Client *Party
	// Projects the report covers; none means all of them.
	Projects  []ProjectRef
	Title     string
	Summary   string
	Kind      string
	From      time.Time
	To        time.Time
	Status    string
	SentAt    *time.Time
	PaidAt    *time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
	// Currency is the freelancer's while it's a draft, frozen once sent.
	Currency string
	// ItemCount and ListTotal (the sum of the prices, nil when none has one)
	// are filled whenever a report is read; Items when loading one report.
	ItemCount int
	ListTotal *int64
	Items     []Item
}

// ProjectNames lists the report's projects: "Website, Branding"; "" for all.
func (r Report) ProjectNames() string {
	names := make([]string, 0, len(r.Projects))
	for _, p := range r.Projects {
		names = append(names, p.Name)
	}
	return strings.Join(names, ", ")
}

// Total sums the items' prices; ok is false when no item has a price.
func (r Report) Total() (cents int64, ok bool) {
	for _, it := range r.Items {
		if it.PriceCents != nil {
			cents += *it.PriceCents
			ok = true
		}
	}
	return cents, ok
}
