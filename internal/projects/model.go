// Package projects groups tasks. A project is shared between a freelancer and
// a client they are linked with, or is a freelancer's personal project with no
// client. Either side can create one; its creator edits and deletes it.
package projects

import (
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("project not found")
	// ErrNotLinked is returned when a project is created for someone the
	// creator doesn't work with.
	ErrNotLinked = errors.New("not linked with this person")
	// ErrNotOwner is returned when someone other than the creator edits or
	// deletes a project.
	ErrNotOwner = errors.New("only the project's creator can change it")
	// ErrContractor is returned when a client company's contractor starts a
	// project: they work in the company's projects, not shape them.
	ErrContractor = errors.New("contractors can't start projects")
	// ErrNotInvitable is returned when the author invites someone who isn't
	// the project's freelancer or one of the client company's people.
	ErrNotInvitable = errors.New("this person can't be invited to the project")
	// ErrKeepAuthor is returned when the project's author would be removed.
	ErrKeepAuthor = errors.New("the project's author stays")
	// ErrUnassignedTasks is returned when deleting a project with tasks that
	// wait for a freelancer: off the project they'd have no board.
	ErrUnassignedTasks = errors.New("the project has tasks without a freelancer")
	// ErrNotProjectFreelancer is returned when someone other than a freelancer
	// on the project sets a rate for it.
	ErrNotProjectFreelancer = errors.New("only a freelancer on the project sets a rate for it")
)

// Person is someone who can be on a project: its freelancer, the client, or a
// member or contractor of the client's company.
type Person struct {
	ID    string
	Name  string
	Email string
	// Role is "freelancer", "owner", "member" or "contractor".
	Role string
}

// PersonalFilter is the counterpart filter value that selects personal projects.
const PersonalFilter = "personal"

// Party is one side of a project, or someone on it.
type Party struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
	// Role is set for a participant: "freelancer", "owner", "member" or "contractor".
	Role string `json:"role,omitempty"`
}

// CanSetRate reports whether the viewer may set an hourly rate on the
// project: a freelancer on it, for their own work.
func CanSetRate(p Project, viewerID string) bool {
	for _, person := range p.Participants {
		if person.ID == viewerID && person.Role == "freelancer" {
			return true
		}
	}
	return false
}

// Project groups tasks between the same freelancer and client.
type Project struct {
	ID         string
	Freelancer Party
	// Client is nil for a freelancer's personal project.
	Client      *Party
	CreatedBy   string
	Name        string
	Description string
	OpenTasks   int
	TotalTasks  int
	// Participants are the people on the project, the author first.
	Participants []Party
	// MyRateCents is the viewer's hourly rate on this project, which their
	// tasks in it are billed at instead of their own; nil when they set none.
	MyRateCents *int64
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
