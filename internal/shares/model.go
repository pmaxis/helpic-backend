// Package shares lets a task be shown by a link: whoever opens it reads the
// task — its title, description, status and due date — with its subtasks and
// files, without an account. Nothing else goes out: not the people on it, the
// price, the time, the comments or the history. The task's creator or its
// freelancer turns the link on, and off again; a new link after that is a
// new address.
package shares

import (
	"errors"
	"time"
)

var (
	// ErrNotFound is returned for a link that doesn't exist or was turned off.
	ErrNotFound = errors.New("shared task not found")
	// ErrNotAllowed is returned when someone other than the task's creator or
	// its freelancer manages its link.
	ErrNotAllowed = errors.New("only the task's creator or its freelancer shares it")
)

// Share is a task's public link.
type Share struct {
	TaskID    string
	Token     string
	CreatedAt time.Time
}

// Task is a task as its link shows it.
type Task struct {
	ID          string
	Title       string
	Description string
	// Status is the bucket (new, in_progress, done); StatusName the column.
	Status     string
	StatusName string
	DueOn      *time.Time
	Archived   bool
}

// Subtask is a subtask under a shared task.
type Subtask struct {
	Title      string
	Status     string
	StatusName string
}
