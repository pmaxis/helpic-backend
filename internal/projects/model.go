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
)

// PersonalFilter is the counterpart filter value that selects personal projects.
const PersonalFilter = "personal"

// Party is one side of a project.
type Party struct {
	ID    string
	Name  string
	Email string
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
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
