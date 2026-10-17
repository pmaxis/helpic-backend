// Package tasks holds the work between a freelancer and a client they are
// linked with, and a freelancer's personal tasks that have no client. Either
// side can create a task; its creator and its freelancer edit it, the
// freelancer moves it through the work statuses.
package tasks

import (
	"errors"
	"slices"
	"time"

	"github.com/pmaxis/helpic/internal/user"
)

var (
	ErrNotFound = errors.New("task not found")
	// ErrNotLinked is returned when a task is created for someone the creator
	// doesn't work with.
	ErrNotLinked = errors.New("not linked with this freelancer")
	// ErrNotEditable is returned when deleting a task in a status that no
	// longer allows it.
	ErrNotEditable = errors.New("task cannot be changed in its current status")
	// ErrEditNotAllowed is returned when someone other than the task's
	// creator or freelancer edits it.
	ErrEditNotAllowed = errors.New("only the creator or the freelancer edits a task")
	// ErrProjectNotFound is returned for a project the user doesn't take part in.
	ErrProjectNotFound = errors.New("project not found")
	// ErrProjectMismatch is returned when a task is moved to a project of
	// another freelancer–client pair.
	ErrProjectMismatch = errors.New("project belongs to someone else")
	// ErrStatusNotFound is returned for an unknown workflow status.
	ErrStatusNotFound = errors.New("status not found")
	// ErrStatusMismatch is returned for a status of another freelancer–client
	// pair's workflow.
	ErrStatusMismatch = errors.New("status belongs to another board")
	// ErrTransition is returned for a status change the viewer may not make.
	ErrTransition = errors.New("status change not allowed")
	// ErrPriceNotAllowed is returned when anyone but the task's freelancer
	// sets its price.
	ErrPriceNotAllowed = errors.New("only the freelancer sets the price")
	// ErrLabelMismatch is returned for a label of another pair's board.
	ErrLabelMismatch = errors.New("label belongs to another board")
)

// PersonalFilter is the CounterpartID value that selects personal tasks.
const PersonalFilter = "personal"

// Status categories. Every workflow status (a board column) belongs to one;
// Task.Status holds the category, Task.StatusID the exact status.
const (
	StatusNew        = "new"
	StatusInProgress = "in_progress"
	StatusDone       = "done"
	StatusCancelled  = "cancelled"
)

// Priorities, least urgent first. A task starts with none.
const (
	PriorityNone   = "none"
	PriorityLow    = "low"
	PriorityMedium = "medium"
	PriorityHigh   = "high"
	PriorityUrgent = "urgent"
)

// Priorities lists every priority, least urgent first.
var Priorities = []string{PriorityNone, PriorityLow, PriorityMedium, PriorityHigh, PriorityUrgent}

// Statuses lists every task status in workflow order.
var Statuses = []string{StatusNew, StatusInProgress, StatusDone, StatusCancelled}

// transitions says which status changes each role may make on a shared task,
// from each status. The freelancer moves the work freely between new, in
// progress and done; the client cancels, restores, or sends a done task back
// for more work. A freelancer may also cancel and restore tasks they created
// themselves (see creatorTransitions). Personal tasks have no limits.
var transitions = map[string]map[string][]string{
	user.RoleFreelancer: {
		StatusNew:        {StatusInProgress, StatusDone},
		StatusInProgress: {StatusNew, StatusDone},
		StatusDone:       {StatusNew, StatusInProgress},
	},
	user.RoleClient: {
		StatusNew:        {StatusCancelled},
		StatusInProgress: {StatusCancelled},
		StatusDone:       {StatusInProgress},
		StatusCancelled:  {StatusNew},
	},
}

// creatorTransitions are extra changes the task's creator may make on top of
// their role's.
var creatorTransitions = map[string][]string{
	StatusNew:        {StatusCancelled},
	StatusInProgress: {StatusCancelled},
	StatusCancelled:  {StatusNew},
}

// NextStatuses returns the statuses the viewer may move the task to. A
// personal task is the freelancer's alone, so any status goes.
func NextStatuses(t Task, role, viewerID string) []string {
	if t.Client == nil {
		out := []string{}
		for _, s := range Statuses {
			if s != t.Status {
				out = append(out, s)
			}
		}
		return out
	}
	out := slices.Clone(transitions[role][t.Status])
	if t.CreatedBy == viewerID {
		for _, s := range creatorTransitions[t.Status] {
			if !slices.Contains(out, s) {
				out = append(out, s)
			}
		}
	}
	return out
}

// CanTransition reports whether the viewer may move the task to the status.
func CanTransition(t Task, role, viewerID, to string) bool {
	return slices.Contains(NextStatuses(t, role, viewerID), to)
}

// CanEdit reports whether the viewer may edit the task's content: its
// creator, or its freelancer (who also edits what a client set), in any
// status — the history keeps every change.
func CanEdit(t Task, viewerID string) bool {
	return t.CreatedBy == viewerID || t.Freelancer.ID == viewerID
}

// CanDelete reports whether the viewer may delete the task: only its creator,
// and only before work has started.
func CanDelete(t Task, viewerID string) bool {
	return t.CreatedBy == viewerID && t.Status == StatusNew
}

// CanSeePrice reports whether the viewer sees the task's price: its
// freelancer and its client — the company's owner — do; the owner's team
// members don't.
func CanSeePrice(t Task, viewerID string) bool {
	return t.Client == nil || viewerID == t.Freelancer.ID || viewerID == t.Client.ID
}

// CanSetPrice reports whether the viewer may set the task's price: its
// freelancer may, at any time.
func CanSetPrice(t Task, viewerID string) bool {
	return t.Freelancer.ID == viewerID
}

// Party is one side of a task.
type Party struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// Label is a tag on a task, from its pair's set.
type Label struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

// ProjectRef names the project a task belongs to.
type ProjectRef struct {
	ID   string
	Name string
}

// Task is a piece of work a client set for a freelancer.
type Task struct {
	ID         string
	Freelancer Party
	// Client is nil for a freelancer's personal task.
	Client *Party
	// Project is nil for a task outside any project.
	Project   *ProjectRef
	CreatedBy string
	// Creator is who set the task: either side, or a member of the client's
	// company.
	Creator Party
	// Participants are who took part in the task: its freelancer first, then
	// everyone who set it, commented, attached a file or changed it, in the
	// order they first did.
	Participants []Party
	// StatusID and StatusName are the task's exact workflow status (its
	// board column); Status is that status's category.
	StatusID   string
	StatusName string
	// Position is the task's place in its column; nil until someone orders it.
	Position    *int
	Title       string
	Description string
	Status      string
	DueOn       *time.Time
	// PriceCents is nil while the task has no price; Currency is the
	// freelancer's.
	PriceCents *int64
	Currency   string
	Priority   string
	// Labels are sorted by name.
	Labels    []Label
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Filter narrows a task list.
type Filter struct {
	// Statuses keeps tasks in any of these statuses; empty means all.
	Statuses []string
	// CounterpartID keeps tasks with one client (for a freelancer) or one
	// freelancer (for a client). PersonalFilter keeps a freelancer's personal
	// tasks.
	CounterpartID string
	// ProjectID keeps one project's tasks.
	ProjectID string
}
